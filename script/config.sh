#!/bin/bash

# Benchmark client: benchcli-uwscpp (default) or benchcli-go.
# Override for one run with: BENCH_CLIENT=benchcli-go bash script/benchmark.sh
BENCH_CLIENT=${BENCH_CLIENT:-benchcli-uwscpp}
case "$BENCH_CLIENT" in
    benchcli-go|benchcli-uwscpp) ;;
    *) echo "Unsupported BENCH_CLIENT: $BENCH_CLIENT" >&2; return 1 ;;
esac

# Goroutine pool the servers run their callbacks on. The taskpool package
# registers inline, go, fib_adaptive, fib_cond, fib_elastic, fnet, greatws, 
# nbio and uws; "default" is not one of them but leaves each framework on
# the scheduling it ships with.
# Override for one run with: BENCH_TASKPOOL=nbio bash script/benchmark.sh
BENCH_TASKPOOL=${BENCH_TASKPOOL:-fib_adaptive}
# Pool sizing. 0 leaves each pool its own default, which is the sizing the
# framework it came from runs it at.
BENCH_TASKPOOL_MIN=${BENCH_TASKPOOL_MIN:-0}
BENCH_TASKPOOL_MAX=${BENCH_TASKPOOL_MAX:-0}
BENCH_TASKPOOL_QUEUE=${BENCH_TASKPOOL_QUEUE:-0}

# The servers that take the -taskpool flags. The rest have no pool to swap
# and would exit on a flag they do not define.
taskpool_frameworks=(
    "fib"
    "fnet"
    "greatws"
    "greatws_event"
    "nbio_mixed"
    "nbio_nonblocking"
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
