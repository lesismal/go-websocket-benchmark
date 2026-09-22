# uwebsockets

Echo WebSocket server built on [uWebSockets](https://github.com/uNetworking/uWebSockets)
v20.74.0 (the same pinned version and uSockets submodule used by `benchcli-uwscpp`), using
the high-level `uWS::App` server API instead of the low-level protocol headers the C++
benchmark client uses. One `uWS::App`/event loop runs per hardware thread; every loop
listens on all of the framework's benchmark ports, relying on uSockets' default
`SO_REUSEPORT` behavior to spread accepted connections across threads.

`/init`, `/ps` and `/taskpool` replicate just enough of `frameworks.HandleCommon` (see
`frameworks/handlers.go`) for the benchmark clients' CPU/RSS and pool reporting, sampling
`/proc/self/stat` and `/proc/self/status` directly instead of linking `gopsutil`. They are
all registered on every benchmark port rather than a separate pid port, so
`config.Ports["uwebsockets"]`'s last port doubles as the control port (no entry needed in
`config.InitAndGetFrameworkPid`'s pid-port-offset list).

## Task pool

Like the Go servers (see [`taskpool`](../../taskpool)), this one runs its message callback off
the reactor: a thread pool takes each connection's frames, and the loop threads keep the reads,
the parse and the writes.

`-taskpool` takes the same names the Go servers take, and it defaults the same way
(`fib_adaptive`). None of those names is anything a C++ server can run, so what this one reads
out of the name is **where a server answers from**: `default` and `inline` install no pool,
which for uWS means the event loop, and every other mode hands the callback to a goroutine off
the loop, which this server's thread pool stands in for.

| `BENCH_TASKPOOL` / `-taskpool` | Go side | here |
| --- | --- | --- |
| `default` | no pool installed: each framework keeps the scheduling it ships with | the loop callback, since uWS's own scheduling is the event loop |
| `inline` | no pool: the callback runs on the I/O goroutine that read the frame | the loop callback, likewise |
| `go`, `fib_adaptive`, `fib_cond`, `fib_elastic`, `nbio`, `fnet`, `greatws`, `uws` | a pool, off the event loop | the pool |
| `pool` | - | the pool, asked for directly rather than through what a Go mode implies |
| anything else | - | the server exits, as `taskpool.FromFlags` does on a name that names no pool |

The two in-loop modes are not the same thing on the Go side - `default` leaves each framework
the scheduling it ships with, which is a pool for most of them, while `inline` puts them all on
the reading goroutine - but neither installs a pool, and for this server not installing one
means the loop callback. That is also what makes `default` the mode to compare against
`greatws_event`, the one Go server that answers in its poller under it.

The startup line says which way the flag was read, and the `/taskpool` route serves the same
decision for the report's Pool column (`fib_adaptive(pool)`, `default(loop)`):

```
uwebsockets taskpool: fib_adaptive -> pool (Go side: fib's taskpool, adaptive mode, off the
event loop; here a thread pool stands in for it) min=0(ignored) max=0 queue=0 workers=14
shards=14 pending=65536 rejects=true loops=14 hardware_concurrency=14
```

`-tpmax` sets the worker count and `-tpqueue` the queued connections (65536 by default, as for
the `uws` pool). `-tpmin` is accepted and ignored: the workers are all started up front. The
default is one worker per core rather than the Go pools' hundreds because these are OS threads
running a callback that never blocks, and because the pool sits on top of the loop threads -
the process runs 2x `hardware_concurrency` threads with the pool where the in-loop modes run
1x.

One connection's messages are handled and answered in the order they arrived, the way the Go
frameworks manage it: the connection carries a queue of frames and a drain flag, and a drain is
submitted only when none is in flight, so the pool never holds two tasks for the same
connection. What the pool takes as a task is the connection itself, as fib's pool does.

Two things the Go pools do not have to deal with:

- uWS is single threaded per loop, so a worker cannot write: it hands the echo back through
  `uWS::Loop::defer`, whose queue is FIFO. Every send goes through that queue, including the
  drains the pool refuses and runs on the loop thread, because the drain flag goes down before
  the loop has run the sends deferred for it - a batch that sent directly could overtake one
  still queued.
- `std::string_view` from the message callback points into the loop's read buffer, which uWS
  reuses as soon as the callback returns, so a frame's payload is copied on the way into the
  queue. fnet's adapter copies for the same reason.

A refused submission (the shard queue is full) is drained on the loop thread instead, which is
what the Go adapters do with a task their pool declines; the server logs the first one.

The queue itself is per-connection state the inline mode does not carry - on the order of 150
bytes a connection, so tens of megabytes of the reported RSS at 50k connections and a couple of
hundred at a million. Worth remembering when comparing the memory column between the two modes.

## Caveats

- uSockets always enables `TCP_NODELAY` on accepted sockets and does not expose a way to
  disable it, so `-nodelay=false` is accepted (for CLI compatibility with the other
  frameworks) but has no effect; the server logs a note when it's passed.
- `/debug/pprof/*` isn't implemented (there's no Go runtime to profile), so
  `-ep=true`/`-rp=true` pprof capture is skipped for this framework.

## Build

```sh
# Debian/Ubuntu prerequisites
sudo apt-get install build-essential git zlib1g-dev

# Build just this server (first build downloads pinned uWebSockets/uSockets).
bash frameworks/uwebsockets/build.sh

# Built automatically as part of the full benchmark.
bash script/benchmark.sh
```

The build caches uWebSockets/uSockets sources in `.deps/` and objects in `.build/`
(both ignored by Git and independent from `benchcli-uwscpp`'s own `.deps`/`.build`).
