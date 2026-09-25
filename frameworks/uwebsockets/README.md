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
`config.InitAndGetFrameworkPid`'s pid-port-offset list). With the logic pool off the server
takes `uwebsockets-inline`'s ports instead, 31101 to 31150; see below.

## Threads

uWS runs one event loop per thread and is not thread safe within a loop, so a loop here is
always single threaded: the parallelism is in how many there are, one `uWS::App` each. The
count comes from the CPUs the process may actually run on - `sched_getaffinity` where there is
one, `hardware_concurrency()` otherwise - because `script/env.sh` pins the server to about half
the host's CPUs with `taskset`, and `hardware_concurrency()` counts every online CPU regardless
of the mask. `-loops` overrides it with a thread count, `-loopspercpu` with a multiplier of
those CPUs (`script/config.sh`'s `BENCH_UWS_LOOPS_PER_CPU`), which is the form that means the
same arrangement on machines of different sizes. The startup lines print both numbers, and
`script/servers.sh` copies the last of them to the benchmark console:

```
uwebsockets benchmark config: loops=5 workers=1 threads=6 cpus=5 hardware_concurrency=10 ports=31001-31050
uwebsockets threads: event loops=5, task pool workers=1, cpus=5
```

With `-logicpool=false` the same lines read `workers=0 threads=5 ... ports=31101-31150` and
`task pool workers=0`.

## Logic thread pool

The message callback can run off the reactor, the way the Go servers run theirs on a pool (see
[`taskpool`](../../taskpool)): a thread pool takes each connection's frames, and the loop
threads keep the reads, the parse and the writes. It is **on by default** (`-logicpool=true`),
and it is what the `uwebsockets` entry measures.

`-logicpool=false` echoes straight from the loop callback instead, uWS's own scheduling, and is
the `uwebsockets-inline` entry: the same binary, which takes that entry's ports (31101 to 31150,
`kInlinePortStart` to `kInlinePortEnd`) when the pool is off, so that both are up in one run the
way the Go servers and their `-inline` entries are. `script/servers.sh` passes `-logicpool=true`
to the one and `-logicpool=false` to the other.

This is the server's own switch, independent of the Go servers' pools: it does not read
`-taskpool` or the `-tp*` flags, and `script/servers.sh` passes it none of them, so
`BENCH_TASKPOOL` and `BENCH_TASKPOOL_MIN/_MAX/_QUEUE` never change what it runs.

The startup line says which way it went, and the `/taskpool` route serves the same for the
report's Pool: `logicpool` with the pool on, `inline` without it.

```
uwebsockets logicpool: on -> thread pool off the event loop workers=1 shards=1 pending=65536
rejects=true loops=5 cpus=5
```

`-workers` sets the worker count, `-workerspercpu` sets it as a multiplier of the CPUs the
process may run on (`round(N * cpus)`, at least one thread), and `-poolqueue` the queued
connections (65536 by default, as for the `uws` pool). All three only matter with the pool on.

`script/config.sh` configures the two multipliers as `BENCH_UWS_WORKERS_PER_CPU` and
`BENCH_UWS_LOOPS_PER_CPU`, and `script/servers.sh` passes them to this server alone - no Go
server defines them. 0, the default for both, leaves the sizing below. Sweeping the pool is
one variable:

```sh
BENCH_UWS_WORKERS_PER_CPU=0.5 BENCH_FRAMEWORKS=uwebsockets bash script/benchmark.sh
```

The pool's workers are OS threads on top of the loop threads. The Go pools can be hundreds of
goroutines because those multiplex onto the `GOMAXPROCS` threads their pollers already run on;
OS threads do not, so the pool's size here is a question of whether its threads come out of the
loops' CPUs or sit on top of them. By default they sit on top: a loop for every CPU, and one
worker per four CPUs (at least one) besides. `-loops`/`-loopspercpu` and `-workers`/`-workerspercpu`
each set their own side only.

Measured in Docker, server and client pinned to disjoint CPU sets the way `script/env.sh` pins
them, `benchcli-uwscpp` echoing a 1KiB payload over 50000 connections at 10000 concurrency; TPS
averaged over two to four runs:

| server CPUs | loops+workers | BenchEcho TPS | BenchRate TPS | |
| --- | --- | --- | --- | --- |
| 2 | 1+1 | 266k | 1.12M | the old default, `loops = cpus - workers` |
| 2 | 2+1 | 323k | 1.43M | the default |
| 2 | 2+2 | 263k | 1.33M | |
| 3 | 2+1 | 357k | 2.17M | the old default |
| 3 | 3+1 | 467k | 2.83M | the default |
| 3 | 4+1 | 464k | 2.63M | |
| 3 | 3+2 | 360k | 2.60M | |
| 5 | 4+1 | 582k | 3.12M | the old default |
| 5 | 5+1 | 585k | 3.15M | the default |
| 5 | 6+1 | 570k | 3.03M | |
| 5 | 4+2 | 526k | 2.97M | |
| 5 | 5+2 | 518k | 3.10M | |
| 5 | `-logicpool=false`, 5 loops | 591k | 4.14M | no pool, for reference |

A loop is worth more than a worker - the loop side does the poll, the read, the frame parse and
the write, while a worker only copies a payload and defers it back, and parks between batches.
So a loop given up to the pool costs a 1/cpus share of the throughput, 30% of it at 3 CPUs,
while the one thread a worker adds on top cost nothing measurable at any size tried. A second
worker was slower at every size. The default at 3 CPUs is the one `script/docker_benchmark.sh`
gives the server on a 10-CPU Docker Desktop, where it moves BenchEcho from 357k to 467k.

An earlier measurement, at 2000 connections with 5 server CPUs, had 4+1 ahead of 5+1 by 7%
(858k against 797k); at 50000 connections the two are within 1%. Sizing by
`hardware_concurrency()` instead of the affinity mask, which built 10+10 threads on that host,
measured 532k there.

Nothing larger than 5 server CPUs was measured, so on a bigger machine the two multipliers are
what to sweep, and the pair of columns to read them against is the server's CPU% and the TPS:
the pool's threads park between batches, so a run that leaves CPU idle is not necessarily a
run that would go faster with more of them.

What remains is the handoff itself: at the same thread count the pool echoes at about 60% of
the in-loop rate in that 2000-connection measurement (858k against 1,375k at four loops), because every batch pays a payload copy,
a cross-thread queue and a `Loop::defer` for work that is otherwise a `memcpy`. That is the
price of answering off the reactor, which is what the Go frameworks are being measured doing;
`-logicpool=false`, the `uwebsockets-inline` entry, is the mode that does not pay it.

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
