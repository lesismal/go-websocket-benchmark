#!/bin/bash

# Benchmark client: benchcli-uwscpp (default) or benchcli-go.
# Override for one run with: BENCH_CLIENT=benchcli-go bash script/benchmark.sh
BENCH_CLIENT=${BENCH_CLIENT:-benchcli-uwscpp}
case "$BENCH_CLIENT" in
    benchcli-go|benchcli-uwscpp) ;;
    *) echo "Unsupported BENCH_CLIENT: $BENCH_CLIENT" >&2; return 1 ;;
esac

# Goroutine pool the servers run their callbacks on. Every value the
# taskpool package takes, and what it selects:
#
#   default       each framework's own scheduling. Not a pool, and not what a
#                 run without this variable measures. greatws_event answers
#                 in its poller under this one value, and uwebsockets in its
#                 event loop; the rest run the pool they ship with
#   inline        no pool: the callback runs on the I/O goroutine that read
#                 the frame, so the answer is written from the event loop
#   go            one goroutine per task, bounded by nothing
#   fib_adaptive  github.com/lesismal/fib/go/taskpool in adaptive mode, which
#                 is fib's own default and this benchmark's
#   fib_cond      the same pool in cond mode: a fixed population of parked
#                 goroutines
#   fib_elastic   the same pool in elastic mode: goroutines forked on demand
#                 up to a ceiling
#   nbio          github.com/lesismal/nbio/taskpool
#   fnet          fnet.WorkerPool, sharded and elastic: workers spawn on
#                 demand and retire when idle
#   greatws       greatws's stream2 business pool
#   uws           the sharded channel executor uws runs on here, and the only
#                 one that refuses work rather than waiting for room
#
# default and inline install no pool; all the rest answer off the event loop,
# which is also how the uwebsockets server reads this variable: see
# taskpool_frameworks below.
#
# Whichever is selected, each report carries a Pool column naming the pool its
# server installed, read from the server's own /taskpool route, so a report
# says which scheduling produced it. "-" there is a framework with no pool
# hook at all.
#
# Override for one run with: BENCH_TASKPOOL=nbio bash script/benchmark.sh
BENCH_TASKPOOL=${BENCH_TASKPOOL:-fib_adaptive}
# Pool sizing. 0 leaves each pool its own default, which is the sizing the
# framework it came from runs it at.
BENCH_TASKPOOL_MIN=${BENCH_TASKPOOL_MIN:-0}
BENCH_TASKPOOL_MAX=${BENCH_TASKPOOL_MAX:-0}
BENCH_TASKPOOL_QUEUE=${BENCH_TASKPOOL_QUEUE:-0}

# The servers that take the -taskpool flags. The rest have no pool to swap
# and would exit on a flag they do not define.
#
# uwebsockets takes them too, but it is a C++ server, so none of the Go pools
# can run under it. It reads the value for what it says about where a server
# answers from: default and inline install no pool, which for uWS means the
# event loop, and every other mode hands the callback to a goroutine off the
# loop, which its own thread pool stands in for. Its Pool column says which
# of the two it did, e.g. "nbio(pool)" or "default(loop)". It exits on a value
# that names no mode, as the Go servers do. See
# frameworks/uwebsockets/README.md.
taskpool_frameworks=(
    "fib"
    "fnet"
    "greatws"
    "greatws_event"
    "nbio_mixed"
    "nbio_nonblocking"
    "uwebsockets"
    "uws_events"
    "uws_std"
)

Connections=(5000 50000)
BodySize=(512 1024)
BenchTime=(2000000)
SleepTime=5

frameworks=(
    "fasthttp"
    "fib"
    "gobwas"
    "greatws_event"
    "greatws"
    "quickws"
    "gorilla"
    "gws"
    "gws_std"
    "hertz"
    "hertz_std"
    "nbio_blocking"
    "nbio_mixed"
    "nbio_nonblocking"
    "nbio_std"
    "nettyws"
    "nhooyr"
    "uws_std"
    "uws_events"
    "fnet"
    "uwebsockets"
)

# Optional comma-separated subset, used by the Docker smoke test and useful for
# focused local runs. Reject unknown names before they reach build paths.
if [ -n "${BENCH_FRAMEWORKS:-}" ]; then
    all_frameworks=("${frameworks[@]}")
    IFS=',' read -r -a requested_frameworks <<< "$BENCH_FRAMEWORKS"
    frameworks=()
    for requested_framework in "${requested_frameworks[@]}"; do
        framework_found=false
        for available_framework in "${all_frameworks[@]}"; do
            if [ "$requested_framework" = "$available_framework" ]; then
                framework_found=true
                break
            fi
        done
        if [ "$framework_found" != true ]; then
            echo "Unsupported framework in BENCH_FRAMEWORKS: $requested_framework" >&2
            return 1
        fi
        frameworks+=("$requested_framework")
    done
    if [ "${#frameworks[@]}" -eq 0 ]; then
        echo "BENCH_FRAMEWORKS must select at least one framework" >&2
        return 1
    fi
fi
