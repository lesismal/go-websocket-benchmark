#!/bin/bash

# Benchmark client: benchcli-uwscpp (default) or benchcli-go.
# Override for one run with: BENCH_CLIENT=benchcli-go bash script/benchmark.sh
BENCH_CLIENT=${BENCH_CLIENT:-benchcli-uwscpp}
case "$BENCH_CLIENT" in
    benchcli-go|benchcli-uwscpp) ;;
    *) echo "Unsupported BENCH_CLIENT: $BENCH_CLIENT" >&2; return 1 ;;
esac

# Where the servers are, as the clients should reach them: an address or a
# hostname, IPv6 included. The default keeps a single-node run on loopback.
# The servers always bind every interface, so a two-node run configures only
# this side.
# Override for one run with: BENCH_SERVER_HOST=10.0.0.2 bash script/benchmark.sh
BENCH_SERVER_HOST=${BENCH_SERVER_HOST:-127.0.0.1}

# Which half of the benchmark this machine runs:
#
#   both    (default) build everything, start the servers, run the clients
#           against them and write the report - one machine, as before
#   server  build and start the servers, then leave them running. Nothing is
#           measured here; the client node does that
#   client  build the client only, run it against BENCH_SERVER_HOST and write
#           the report. Nothing is started or stopped here
#
# A two-node run is BENCH_ROLE=server on one machine and, once it reports the
# servers are up, BENCH_ROLE=client BENCH_SERVER_HOST=<that machine> on the
# other. "client" against a loopback host is also the way to run the clients
# again without restarting servers that are already up on this machine.
#
# Two things differ from a single-node run. The client cannot stop a server it
# did not start, so every framework's server stays up for the whole run rather
# than being killed after its turn: stop them on the server node afterwards
# with script/killall.sh, and use BENCH_FRAMEWORKS below if the idle ones
# holding memory would disturb the framework being measured. And each node
# gives the whole machine to its own half, since there is no longer anything
# to divide it with; BENCH_SERVER_CPU_LIST and BENCH_CLIENT_CPU_LIST still
# pin it where a node shares its CPUs with something else.
BENCH_ROLE=${BENCH_ROLE:-both}
case "$BENCH_ROLE" in
    both|server|client) ;;
    *) echo "Unsupported BENCH_ROLE: $BENCH_ROLE (want both, server or client)" >&2; return 1 ;;
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

# The order the report tables put their rows in. Both orders carry the same
# rows and the same numbers; only the order differs:
#
#   result     (default) best first, ranked by the number each benchmark
#              answers with: TPS for Connections and BenchEcho, and Bytes
#              Recv - what the clients read back off the server - for
#              BenchRate. The rate test writes at a rate the clients set
#              rather than to completion, so what came back under that load is
#              its result there the way TPS is in the other two; Bytes Sent is
#              the load rather than the answer, and Packet Recv counts a small
#              reply the same as a large one
#   framework  the order FrameworkList in config/config.go lists them in,
#              which is the order every report was written in before this
#              variable existed. Note it is not the order the frameworks array
#              below runs them in - the two lists carry the same names in
#              different orders, and only the Go one reaches a report. This is
#              what puts a framework on the same row in every table and across
#              runs, whatever it scored, so two reports can be diffed
#
# Neither order ranks by EER or EchoEER, which divide throughput by the CPU it
# cost and so answer a different question; both are still columns to read.
# Rows that tie keep the framework order between them, so two frameworks that
# scored the same - or a whole table from a benchmark that did not run, which
# leaves every row at zero - come out the same way on every run.
#
# Override for one run with: BENCH_REPORT_SORT=framework bash script/benchmark.sh
# or, without re-running the benchmark, by passing the client flag straight to
# the report step: bash script/report.sh -sort=framework
BENCH_REPORT_SORT=${BENCH_REPORT_SORT:-result}
case "$BENCH_REPORT_SORT" in
    result|framework) ;;
    *) echo "Unsupported BENCH_REPORT_SORT: $BENCH_REPORT_SORT (want result or framework)" >&2; return 1 ;;
esac

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
