# sockudo_ws

Echo WebSocket server built on [sockudo-ws](https://github.com/sockudo/sockudo-ws) 2.1.0, a
Rust WebSocket library on [Tokio](https://tokio.rs), pinned from crates.io together with every
crate under it by `Cargo.lock`.

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
sockudo_ws benchmark config: threads=5 cpus=5 nodelay=true reuseport=true ports=32001-32050
```

`/init` and `/ps` replicate just enough of `frameworks.HandleCommon` (see
`frameworks/handlers.go`) for the benchmark clients' CPU/RSS reporting, sampling
`/proc/self/stat` and `/proc/self/status` the way the `uwebsockets` server does. They are served
on every benchmark port, so `config.Ports["sockudo_ws"]`'s last port doubles as the control
port, and a connection is told apart by its request: an `Upgrade: websocket` request is handed to
sockudo-ws's handshake, anything else is a control request, answered and closed.

## Echo

sockudo-ws's `WebSocketStream` is a futures `Stream` + `Sink`: `Sink::start_send` only encodes a
message into the stream's write buffer, and `poll_flush` writes the buffer out. So the echo loop
feeds every message that is already readable - the ones the last read parsed out along with the
first, and bytes already waiting in the socket - and flushes once for the batch, which is one
vectored write where sending each message would have been one apiece. uWS corks the sends a
message callback makes in the same way. Nothing is waited on in between: a message that is not
there yet ends the batch, and the batch is written before the connection waits for more. The
rate test, which writes several frames to a connection at a time (`-rpl`), is where that shows.

Text messages are UTF-8 validated on the way in, as sockudo-ws does for every `Message::Text`.
Pings are answered by the stream itself.

## Settings

- No idle timeout and no pings of the server's own (sockudo-ws defaults to a 120s idle timeout
  and a ping every 30s of silence). A connection opened in the Connections phase sits idle until
  the echo phase, and the clients do not all answer pings. The `uwebsockets` server turns off
  the same two things in uWS.
- 64MiB for a message and a frame, as `uwebsockets` sets uWS's `maxPayloadLength`.
- No compression.
- `-nodelay` sets `TCP_NODELAY` on each accepted socket, and `-reuseport` `SO_REUSEPORT` on
  each listener, as those flags do for the Go servers; both default to true. The other flags
  `script/servers.sh` hands every server - `-b`, `-m` - are logged and ignored: sockudo-ws sizes
  its buffers itself.
- No `-taskpool` flag, so this server is not in `script/config.sh`'s `taskpool_frameworks`, has
  no `/taskpool` route, and reports `-` for its Pool, as the Go frameworks without a pool hook
  do. Where it answers from is the task that read the frame, which is what `-taskpool=inline`
  means for a Go server.
- The release profile is sockudo-ws's own: `opt-level = 3`, fat LTO, one codegen unit,
  `panic = "abort"`. The build targets the generic CPU of the host's architecture, the way the
  Go servers and the `uwebsockets` build do; `RUSTFLAGS="-C target-cpu=native"` in the
  environment of `script/build.sh` builds for the host CPU instead.

## Memory

sockudo-ws gives every connection a 64KiB read buffer and a 16KiB write buffer, and takes no
size for either. The read buffer is only reserved at first, but each read lands after the last
one until the buffer is used up, so once 64KiB have passed through a connection all of it is
resident - about 80KiB a connection for the two, before any batch grows the write buffer. That
is most of what separates this server from the others in the memory column. Measured in a
container with 3 CPUs for the server, 2000 connections and a 1KiB payload:

| | BenchEcho MEM Avg | BenchRate MEM Avg |
| --- | --- | --- |
| sockudo_ws | 164M | 311M |
| gws | 53M | 54M |
| uwebsockets | 6M | 17M |

At `script/config.sh`'s 50000 connections that is on the order of 4GiB, where the Docker
runner's default memory limit - 80% of what Docker has - is what to check on a small machine.

## Build

```sh
# Needs cargo with edition 2024 support (Rust 1.85+; built and tested with 1.98).
bash frameworks/sockudo_ws/build.sh

# Built automatically as part of the full benchmark.
bash script/benchmark.sh
```

The first build downloads the pinned crates from crates.io; `--locked` makes a build fail
rather than move to other versions. `script/Dockerfile.benchmark` installs the toolchain and
fetches the crates while building the image (`script/docker_rust.sh`), and builds with
`CARGO_NET_OFFLINE=true`, since the benchmark container has no network. Objects go to `target/`,
which Git and the Docker build context both ignore.

Upgrading sockudo-ws is a version bump in `Cargo.toml` and `cargo update -p sockudo-ws`, which
rewrites `Cargo.lock`.
