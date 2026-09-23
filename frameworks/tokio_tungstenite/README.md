# tokio_tungstenite

Echo WebSocket server built on [tokio-tungstenite](https://github.com/snapview/tokio-tungstenite)
0.30.0, the [Tokio](https://tokio.rs) binding of tungstenite, pinned from crates.io together with
every crate under it by `Cargo.lock`.

One multi-threaded Tokio runtime runs the whole server: a listener per benchmark port, each
accepting on a task of its own, and a task per connection that does the handshake and then the
echo. It is the Rust counterpart of a Go server's goroutine per connection on `GOMAXPROCS`
threads, and Tokio's work-stealing scheduler is what spreads the connections over the workers,
the way Go's scheduler spreads the goroutines. There is one worker thread per CPU the process
may run on: `std::thread::available_parallelism` reads the affinity mask (and a cgroup CPU
quota), so it counts the CPUs `script/env.sh` pins the server to rather than the whole host,
which is also the count `GOMAXPROCS` follows. `-threads=N` overrides it; nothing in the
scripts sets it. The startup line prints both:

```
tokio_tungstenite benchmark config: threads=5 cpus=5 nodelay=true reuseport=true ports=32001-32050
```

`/init` and `/ps` replicate just enough of `frameworks.HandleCommon` (see
`frameworks/handlers.go`) for the benchmark clients' CPU/RSS reporting, sampling
`/proc/self/stat` and `/proc/self/status` the way the `uwebsockets` server does. They are served
on every benchmark port, so `config.Ports["tokio_tungstenite"]`'s last port doubles as the
control port. A connection's request head is peeked at rather than read to tell the two apart:
an `Upgrade: websocket` request is handed, still unread, to tungstenite's
`accept_async_with_config`, so the handshake is the library's; anything else is a control
request, read, answered and closed.

## Echo

tokio-tungstenite's `WebSocketStream` is a futures `Stream` + `Sink`: `Sink::start_send` only
frames a message into tungstenite's write buffer, and `poll_flush` writes the buffer out. So the
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
  16MiB). The buffers stay as the library ships them - see Memory below.
- tungstenite sends no pings and has no idle timeout of its own, so a connection opened in the
  Connections phase stays up however long it sits idle before the echo phase, as the
  `uwebsockets` server has uWS keep it.
- No compression: tungstenite does not implement permessage-deflate.
- `-nodelay` sets `TCP_NODELAY` on each accepted socket, and `-reuseport` `SO_REUSEPORT` on
  each listener, as those flags do for the Go servers; both default to true. The other flags
  `script/servers.sh` hands every server - `-b`, `-m` - are logged and ignored.
- No `-taskpool` flag, so this server is not in `script/config.sh`'s `taskpool_frameworks`, has
  no `/taskpool` route, and reports `-` for its Pool, as the Go frameworks without a pool hook
  do. Where it answers from is the task that read the frame, which is what `-taskpool=inline`
  means for a Go server.
- Release profile: `opt-level = 3`, fat LTO, one codegen unit, `panic = "abort"`. The build
  targets the generic CPU of the host's architecture, the way the Go servers and the
  `uwebsockets` build do; `RUSTFLAGS="-C target-cpu=native"` in the environment of
  `script/build.sh` builds for the host CPU instead.

## Memory

tungstenite reserves a 128KiB read buffer for every connection (`read_buffer_size`), and
gathers writes up to 128KiB (`write_buffer_size`) before they go out. Every read zero-fills the
part of the read buffer it reads into, up to that 128KiB, before handing it to the socket - so
all of it is resident as soon as a connection has read once, and each read also costs a 128KiB
`memset`. That is what an application on the library gets unless it sizes the buffer itself -
tungstenite's documentation suggests 4KiB where there are many connections and little read load
- and it is most of what separates this server from the others in the memory column. Measured in
a container with 3 CPUs for the server, 10000 connections and a 1KiB payload:

| | BenchEcho MEM Avg | BenchRate MEM Avg |
| --- | --- | --- |
| tokio_tungstenite | 1.37G | 1.52G |
| gws | 169M | 206M |
| fib | 43M | 45M |
| uwebsockets | 17M | 77M |

That is about 140KiB a connection, so at `script/config.sh`'s 50000 connections on the order of
7GiB, where the Docker runner's default memory limit - 80% of what Docker has - is what to check
on a small machine. The same run put it second of the four on BenchEcho TPS (484k, against fib's
507k, gws's 478k and uwebsockets' 354k), with its server CPU at 299%.

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
