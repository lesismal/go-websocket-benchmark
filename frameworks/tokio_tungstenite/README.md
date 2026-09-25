# tokio_tungstenite

Echo WebSocket server built on [tokio-tungstenite](https://github.com/snapview/tokio-tungstenite)
0.30.0, the [Tokio](https://tokio.rs) binding of tungstenite, pinned from crates.io together with
every crate under it by `Cargo.lock`.

The server is a set of event loops: one current-thread Tokio runtime per CPU the process may
run on, each driven by a thread of its own, the way uWS and the Go event loop servers have a loop
per poller. A listener per benchmark port accepts on one of the loops, and hands every connection
it accepts to the next loop round-robin, which registers the socket with its own reactor and
serves it - the handshake, every read and every write - for the rest of its life. There is no
work stealing between the loops. `std::thread::available_parallelism` reads the affinity mask
(and a cgroup CPU quota), so the loops count the CPUs `script/env.sh` pins the server to rather
than the whole host, which is also the count `GOMAXPROCS` follows. `-threads=N` overrides it;
nothing in the scripts sets it. The startup line prints what was built:

```
tokio_tungstenite benchmark config: loops=5 workers=1 cpus=5 logicpool=true nodelay=true reuseport=true ports=32001-32050
```

## Logic thread pool

`-logicpool=true`, the default, runs the message callback off the loops, on a pool of worker
threads (`src/pool.rs`) built the way the `uwebsockets` server's logic pool is: the workers start
up front, each with a queue of its own, and a connection always hands its batches to the same
one, which keeps its messages in order. The loop reads a batch - every message already readable,
as the echo below takes them - and queues it, and writes out the answers the worker sends back
down the connection's own channel, several at once where several are back. Reading does not
wait for an answer, so a connection can have a batch on the pool while the next one arrives.
There is one worker per four CPUs, and at least one, on top of the loops rather than taken from
them - the `uwebsockets` server's default, which is the best of what that server measured;
`-workers=N` sets it. That is the `tokio_tungstenite` entry, and its Pool is `logicpool`.

`-logicpool=false` answers on the loop that read the frame, and is the
`tokio_tungstenite-inline` entry: the same binary, which takes that entry's ports (32101 to
32150) when the pool is off, so that both are up in one run the way the Go servers and their
`-inline` entries are. Its Pool is `inline`. `script/servers.sh` passes `-logicpool=true` to the
one and `-logicpool=false` to the other, and neither takes the Go servers' `-taskpool` flags.

`/init`, `/ps` and `/taskpool` replicate just enough of `frameworks.HandleCommon` (see
`frameworks/handlers.go`) for the benchmark clients' CPU/RSS and pool reporting, sampling
`/proc/self/stat` and `/proc/self/status` the way the `uwebsockets` server does. They are served
on every benchmark port, so `config.Ports["tokio_tungstenite"]`'s last port doubles as the
control port. A connection's request head is peeked at rather than read to tell the two apart:
an `Upgrade: websocket` request is handed, still unread, to tungstenite's
`accept_async_with_config`, so the handshake is the library's; anything else is a control
request, read, answered and closed.

## Echo

tokio-tungstenite's `WebSocketStream` is a futures `Stream` + `Sink`: `Sink::start_send` only
frames a message, and `poll_flush` writes what has gathered out (here the frames gather under
tungstenite, in the socket wrapper - see Memory below). So the
echo loop feeds every message that is already readable - the ones the last read parsed out along
with the first, and bytes already waiting in the socket - and flushes once for the batch, which
is one write where sending each message would have been one apiece. uWS corks the sends a message
callback makes in the same way. Nothing is waited on in between: a message that is not there yet
ends the batch, and the batch is written before the connection waits for more. The rate test,
which writes several frames to a connection at a time (`-rpl`), is where that shows.

Text messages are UTF-8 validated on the way in, as tungstenite does for every `Message::Text`.
Pings and Closes are answered by tungstenite itself.

## Settings

- tungstenite's `WebSocketConfig` defaults, but for the payload limits: 64MiB for a message and
  for a frame, as `uwebsockets` sets uWS's `maxPayloadLength` (tungstenite's own frame limit is
  16MiB), and for the buffers: a 4KiB read buffer, and a write buffer of 0 - see Memory below.
- tungstenite sends no pings and has no idle timeout of its own, so a connection opened in the
  Connections phase stays up however long it sits idle before the echo phase, as the
  `uwebsockets` server has uWS keep it.
- No compression: tungstenite does not implement permessage-deflate.
- `-nodelay` sets `TCP_NODELAY` on each accepted socket, and `-reuseport` `SO_REUSEPORT` on
  each listener, as those flags do for the Go servers; both default to true. The other flags
  `script/servers.sh` hands every server - `-b`, `-m` - are logged and ignored.
- No `-taskpool` flag: none of the Go pools can run under a Rust server. It is in
  `script/config.sh`'s `taskpool_frameworks` for its `/taskpool` route, which answers
  `logicpool` or `inline`; see Logic thread pool above.
- Release profile: `opt-level = 3`, fat LTO, one codegen unit, `panic = "abort"`. The build
  targets the generic CPU of the host's architecture, the way the Go servers and the
  `uwebsockets` build do; `RUSTFLAGS="-C target-cpu=native"` in the environment of
  `script/build.sh` builds for the host CPU instead.

## Memory

As tungstenite ships, a connection keeps a 128KiB read buffer (`read_buffer_size`) and
zero-fills the part of it each read goes into, up to all 128KiB, before handing it to the
socket - so all of it is resident as soon as a connection has read once, and every read pays a
128KiB `memset`. Its write buffer gathers frames up to 128KiB (`write_buffer_size`) and never
shrinks, so every connection that has echoed a burst keeps its peak. At 50000 connections that
was 6.85G in BenchEcho and 8.9G in BenchPipeline, in a 12.48GB container that also has to hold the
client - enough for the kernel to kill one of the two in BenchPipeline. None of it was a leak: it
is what those buffers hold on to by design.

So the socket under each `WebSocketStream` is a wrapper (`src/stream.rs`) that does what uWS does
with its buffers:

- Reads go through one 512KiB buffer per loop thread, so one `recv` takes all a socket has,
  and tungstenite, its read buffer set to 4KiB - the size its documentation suggests where there
  are many connections - reads from that 4KiB at a time. What it has not taken yet waits in a
  stash from a per-thread pool, which goes back as soon as it is empty.
- With `write_buffer_size` at 0, tungstenite hands each frame to the wrapper as it frames it,
  and the frames gather there in a pooled buffer until the echo loop's flush writes the batch in
  one send and hands the buffer back. Past 64KiB unwritten, a write goes out before more gathers,
  so a client that stops reading stops the echo loop rather than growing it.
- The 8KiB buffer the request head is peeked into is dropped before the connection starts
  echoing, not held for its life as part of the task's state.

The WebSocket side is still tungstenite's: the framing, the parsing, the handshake, and the
answers to pings and closes, which go out through the same wrapper. Measured in Docker with 3
CPUs for the server and 4 for `benchcli-uwscpp`, 50000 connections and a 1KiB payload, averaged
over three runs:

| | BenchEcho TPS | BenchEcho MEM | BenchPipeline TPS | BenchPipeline MEM Avg | BenchPipeline MEM Max |
| --- | --- | --- | --- | --- | --- |
| tungstenite's buffers | 315k | 6.85G | 2.84M | 7.93G | 8.87G |
| these | 454k | 403M | 2.85M | 723M | 900M |

The server's CPU was the same in both, pinned in BenchEcho: the `memset` alone was costing it
three echoes in ten.

## Build

```sh
# Needs cargo with edition 2024 support (Rust 1.85+; built and tested with 1.98).
bash frameworks/tokio_tungstenite/build.sh

# Built automatically as part of the full benchmark.
bash script/benchmark.sh
```

The first build downloads the pinned crates from crates.io; `--locked` makes a build fail
rather than move to other versions. `script/Dockerfile.benchmark` installs the toolchain and
fetches the crates while building the image (`script/docker_rust.sh`), and builds with
`CARGO_NET_OFFLINE=true`, since the benchmark container has no network. Objects go to `target/`,
which Git and the Docker build context both ignore.

Upgrading tokio-tungstenite is a version bump in `Cargo.toml` and
`cargo update -p tokio-tungstenite`, which rewrites `Cargo.lock`.
