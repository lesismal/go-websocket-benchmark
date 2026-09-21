#!/bin/bash

# Benchmark client: benchcli-uwscpp (default) or benchcli-go.
# Override for one run with: BENCH_CLIENT=benchcli-go bash script/benchmark.sh
BENCH_CLIENT=${BENCH_CLIENT:-benchcli-uwscpp}
case "$BENCH_CLIENT" in
    benchcli-go|benchcli-uwscpp) ;;
    *) echo "Unsupported BENCH_CLIENT: $BENCH_CLIENT" >&2; return 1 ;;
esac

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
