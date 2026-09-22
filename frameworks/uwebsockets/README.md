# uwebsockets

Echo WebSocket server built on [uWebSockets](https://github.com/uNetworking/uWebSockets)
v20.74.0 (the same pinned version and uSockets submodule used by `benchcli-uwscpp`), using
the high-level `uWS::App` server API instead of the low-level protocol headers the C++
benchmark client uses. Every loop listens on all of the framework's benchmark ports, relying
on uSockets' default `SO_REUSEPORT` behavior to spread accepted connections across threads.

`/init`, `/ps` and `/taskpool` replicate just enough of `frameworks.HandleCommon` (see
`frameworks/handlers.go`) for the benchmark clients' CPU/RSS and pool reporting, sampling
`/proc/self/stat` and `/proc/self/status` directly instead of linking `gopsutil`. They are
all registered on every benchmark port rather than a separate pid port, so
`config.Ports["uwebsockets"]`'s last port doubles as the control port (no entry needed in
`config.InitAndGetFrameworkPid`'s pid-port-offset list).

## Threads

uWS runs one event loop per thread and is not thread safe within a loop, so a loop here is
always single threaded: the parallelism is in how many there are, one `uWS::App` each. The
count comes from the CPUs the process may actually run on - `sched_getaffinity` where there is
one, `hardware_concurrency()` otherwise - because `script/env.sh` pins the server to about half
the host's CPUs with `taskset`, and `hardware_concurrency()` counts every online CPU regardless
of the mask. `-loops` overrides it with a thread count, `-loopspercpu` with a multiplier of
those CPUs (`script/config.sh`'s `BENCH_UWS_LOOPS_PER_CPU`), which is the form that means the
same arrangement on machines of different sizes. The startup line prints both numbers:

```
uwebsockets benchmark config: loops=4 workers=1 threads=5 cpus=5 hardware_concurrency=10 ports=31001-31050
```

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

`-tpmax` sets the worker count, `-tpmaxpercpu` sets it as a multiplier of the CPUs the process
may run on (`round(N * cpus)`, at least one thread), and `-tpqueue` the queued connections
(65536 by default, as for the `uws` pool). `-tpmin` is accepted and ignored: the workers are
all started up front.

`script/config.sh` configures the two multipliers as `BENCH_UWS_WORKERS_PER_CPU` and
`BENCH_UWS_LOOPS_PER_CPU`, and `script/servers.sh` passes them to this server alone - no Go
server defines them. 0, the default for both, leaves the sizing below. Sweeping the pool is
one variable:

```sh
BENCH_UWS_WORKERS_PER_CPU=0.5 BENCH_FRAMEWORKS=uwebsockets bash script/benchmark.sh
```

The pool's workers are OS threads on top of the loop threads, so the two are sized together:
by default one worker per four CPUs and the loops take the rest, and fixing either side alone -
`-loops`/`-loopspercpu` or `-tpmax`/`-tpmaxpercpu` - leaves the other the remaining CPUs, so a
run cannot end up oversubscribed by accident. Setting both sides is how to ask for more threads
than there are CPUs on purpose. The Go pools can be hundreds of goroutines because those
multiplex onto the `GOMAXPROCS` threads their pollers already run on; OS threads do not, and a
pool that widened the process is what cost this server most of its throughput.

Measured in a container with 5 CPUs for the server and 5 for the client, pinned to disjoint
sets the way `script/env.sh` pins them, echoing a 1KiB payload over 2000 connections; TPS
averaged over three runs:

| server threads | BenchEcho TPS | |
| --- | --- | --- |
| `-taskpool=inline`, loops=5 | 1,525k | no pool, for reference |
| `-taskpool=inline`, loops=4 | 1,375k | |
| loops=4 workers=1 | 858k | the default at 5 CPUs |
| loops=5 workers=1 | 797k | one thread more than there are CPUs |
| loops=4 workers=2 | 776k | |
| loops=3 workers=1 | 670k | |
| loops=3 workers=2 | 513k | |
| loops=10 workers=10 | 532k | what `hardware_concurrency()` sized on this host |

A loop is worth more than a worker - the loop side does the poll, the read, the frame parse and
the write, while a worker only copies a payload and defers it back - and threads beyond the CPU
count cost more than they add. Fixing the sizing is worth about 61% here (532k to 858k).

That is one host, with 5 CPUs for the server; the default is the best of what was measured on
it, not a number that has been checked on a larger machine. On a machine of a different size
the two multipliers are what to sweep, and the pair of columns to read them against is the
server's CPU% and the TPS: the pool's threads park between batches, so a run that leaves CPU
idle is not necessarily a run that would go faster with more of them - every measurement here
that added workers went slower.

What remains is the handoff itself: at the same thread count the pool echoes at about 60% of
the in-loop rate (858k against 1,375k at four loops), because every batch pays a payload copy,
a cross-thread queue and a `Loop::defer` for work that is otherwise a `memcpy`. That is the
price of answering off the reactor, which is what the Go frameworks are being measured doing;
`-taskpool=inline` (or `default`) is the mode that does not pay it.

One thing that did not help, in case it looks obvious: batching the defers. Each finished batch
defers its own send, so a burst across a thousand connections is a thousand `Loop::defer`
calls, each with a lock on the loop's defer queue, a closure allocation and an eventfd write.
Replacing that with one queue per loop that a single deferred flush drains - one wakeup for the
whole burst - measured *slower*, by 1% at 2000 connections and 9% at 20000, interleaving the
two builds run by run:

| | 2000 conns | 20000 conns |
| --- | --- | --- |
| a defer per batch | 848k | 597k |
| one flush per loop | 837k | 544k |

uSockets wakes a loop through an eventfd, and the kernel coalesces that counter on its own, so
the batching saved the write syscalls but not the wakeups - and it added a mutex, a shared
queue and a per-batch `shared_ptr` copy to do it. The defer stayed.

One connection's messages are handled and answered in the order they arrived, the way the Go
frameworks manage it: the connection carries a queue of frames and a drain flag, and a drain is
submitted only when none is in flight, so the pool never holds two tasks for the same
connection. What the pool takes as a task is the connection itself, as fib's pool does.

Two things the Go pools do not have to deal with:

- uWS is single threaded per loop, so a worker cannot write. It appends the finished batch to
  an `Outbox` belonging to that loop, and only the append that finds the outbox unarmed defers
  a flush; the flush then writes everything waiting, in order. Deferring each batch separately
  made every connection in a burst pay its own `Loop::defer` - a lock on the loop's defer
  queue, a heap allocation for the closure, an eventfd write and a loop wakeup, for work that
  is otherwise a `memcpy` - so a burst across a thousand connections cost a thousand wakeups
  where it now costs one. The flush closure captures one pointer, which fits
  `MoveOnlyFunction`'s small-object buffer, so arming allocates nothing either.

  Every send goes through the outbox, including the drains the pool refuses and runs on the
  loop thread, because the drain flag goes down before the loop has written the batches handed
  to it - a batch that sent directly could overtake one still waiting there.
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
