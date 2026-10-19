# aegon

Echo WebSocket server built on [Aegon](https://github.com/UdayKhare09/Aegon), a C++26 web
framework on Linux `io_uring`: multishot accept and recv into provided buffer rings, coroutines,
and one shared-nothing event loop per thread, each with its own `SO_REUSEPORT` listener on every
port. Linux only.

It is set up the way [HttpArena](https://github.com/MDA2AV/HttpArena/tree/main/frameworks/aegon)
runs Aegon, at the Aegon commit HttpArena pins (`e649022`):

- `router.ws("/ws")` with no handler, which is Aegon's batched fast-path echo, answered from the
  event loop that read the frame.
- The `io_uring` ring and provided-buffer pool sized by `RLIMIT_MEMLOCK`: 8192 ring entries and
  16384 4KiB buffers per loop when the limit is 64MB or more (or unlimited), 512 and 256 under
  it.
- One event loop per CPU the process may run on - the smallest of the cgroup CPU quota, the
  affinity mask (`script/env.sh` pins the server with `taskset`) and the online CPUs - each
  thread pinned by Aegon to one of the CPUs in the mask.
- jemalloc as the allocator, which HttpArena preloads; here it is linked in when the build finds
  it.

Of HttpArena's routes only the WebSocket one is kept, on this benchmark's 50 ports
(`config.Ports["aegon"]`, 17701-17750). `-eventloops` sets the loop count instead
(`BENCH_EVENTLOOPS`, which `script/server.sh` passes it), `-nodelay` `TCP_NODELAY` on accepted
sockets as for the Go servers, and every other flag is ignored. The startup lines say what it
built:

```
aegon benchmark config: loops=4 cpus=4 hardware_concurrency=14 nodelay=true ring_entries=8192 buffer_pool_entries=16384 ports=17701-17750
aegon threads: event loops=4 (-eventloops=0, 0 = one per CPU), cpus=4
```

`/init` and `/ps` replicate just enough of `frameworks.HandleCommon` (see
`frameworks/handlers.go`) for the benchmark clients' CPU/RSS reporting, with the same sampler as
the `uwebsockets` server. They are registered on every benchmark port, so the last one doubles
as the control port. There is no `/taskpool`: Aegon has no pool to hand callbacks to, so the
report's Pool reads `-`, and `/debug/pprof/*` isn't there either.

## Memory

The provided buffers are allocated up front: 16384 4KiB buffers is 64MB a loop, so the server's
RSS starts at about 300MB with four loops before a single connection, and grows with the loop
count rather than with the connections. That is what the `MEM` columns show for it.

## Docker

`script/docker_benchmark.sh` runs the container with `--security-opt seccomp=unconfined`, since
Docker's default seccomp profile has blocked the `io_uring` syscalls since Docker 25, and with
`--ulimit memlock=-1:-1`, without which the server would take the small ring and buffer sizes.

The benchmark image's Debian (bookworm) is too old to build Aegon, so `script/Dockerfile.benchmark`
builds it in a stage of its own on the official `gcc:16.2.0-trixie` image (the same GCC HttpArena
gets from Arch Linux; its Arch image has no arm64 build) and copies the result in, bundled with
every shared library it loads, glibc and the dynamic loader included. `build.sh` copies that
server (`AEGON_PREBUILT`) instead of building one.

## Build

```sh
# Debian 13 (trixie) with GCC 15 or newer; Arch Linux has everything in its packages.
sudo apt-get install cmake curl xz-utils libssl-dev liburing-dev libdeflate-dev libjemalloc-dev

# Build just this server (the first build fetches the pinned sources).
bash frameworks/aegon/build.sh

# Built automatically as part of the full benchmark.
bash script/benchmark.sh
```

It needs a C++26 compiler with GCC 15's libstdc++ or newer - Aegon erases from an
`unordered_map` by a heterogeneous key, which GCC 14's does not have - and the development files
of OpenSSL 3.5 or newer, `liburing` and `libdeflate`. `CXX` picks the compiler; by default it is
the first of `g++-16`, `g++-15` and `g++` that compiles that.

Aegon links its HTTP/2 and HTTP/3 stacks into the same library as the WebSocket server, so
those are built too, though only the WebSocket route is served: the build fetches Aegon, glaze
9.0.0 (header only), nghttp2 1.70.0, nghttp3 1.18.0 and ngtcp2 1.25.0 at pinned versions, checks
each against its SHA-256 (trying `GITHUB_MIRROR`'s prefixes before GitHub, as
`script/docker_fetch_deps.sh` does), and builds the last three as static libraries - ngtcp2 with
its OpenSSL backend, which distributions mostly do not ship. Sources are cached in `.deps/` and
objects in `.build/`, both ignored by Git.
