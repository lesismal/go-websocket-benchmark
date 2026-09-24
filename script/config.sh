#!/bin/bash

# Benchmark client: benchcli-uwscpp (default, C++, on uWebSockets), benchcli-rust
# (Rust) or benchcli-go. All three take the same flags and write the same
# reports; see each one's README.
# Override for one run with: BENCH_CLIENT=benchcli-go bash script/benchmark.sh
BENCH_CLIENT=${BENCH_CLIENT:-benchcli-uwscpp}
case "$BENCH_CLIENT" in
    benchcli-go|benchcli-rust|benchcli-uwscpp) ;;
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
# Whichever is selected, each report records the pool its server installed -
# the Pool row of the report's Summary table - read from the server's own
# /taskpool route, so a report says which scheduling produced it. "-" there is
# a framework with no pool hook at all.
#
# Override for one run with: BENCH_TASKPOOL=nbio bash script/benchmark.sh
BENCH_TASKPOOL=${BENCH_TASKPOOL:-fib_adaptive}
# Pool sizing. 0 leaves each pool its own default, which is the sizing the
# framework it came from runs it at.
BENCH_TASKPOOL_MIN=${BENCH_TASKPOOL_MIN:-0}
BENCH_TASKPOOL_MAX=${BENCH_TASKPOOL_MAX:-0}
BENCH_TASKPOOL_QUEUE=${BENCH_TASKPOOL_QUEUE:-0}

# uwebsockets only: how many threads its C++ server builds, as a multiplier of
# the CPUs the server may actually run on (sched_getaffinity, so the taskset
# mask script/env.sh pins it with counts, not the whole host). N here rather
# than a thread count so that one setting means the same arrangement on a
# 4-core laptop and a 64-core server: the count is round(N * cpus), and a
# multiplier that rounds to nothing still gets one thread.
#
#   BENCH_UWS_WORKERS_PER_CPU  the logic thread pool - the workers that run the
#                              message callback off the event loop, which is
#                              what -taskpool puts this server on for every mode
#                              but default and inline
#   BENCH_UWS_LOOPS_PER_CPU    the uWS event loops, one thread each, which do
#                              the poll, the read, the frame parse and the write
#
# 0 (the default for both) leaves the server its own sizing: one loop per CPU,
# and one worker per four CPUs (at least one) on top of them, so the pool adds
# threads rather than taking CPUs from the loops. Setting one of the two changes
# that side only.
#
# Worth knowing before raising the pool: the workers here are OS threads on top
# of the loop threads, not goroutines multiplexed onto the pollers' own threads
# the way every Go pool in this benchmark is, and at 2, 3 and 5 server CPUs a
# second worker came out slower every time, as did giving a loop up for the
# pool (the table is in frameworks/uwebsockets/README.md). Both directions are
# worth a run on a machine of a different size.
#
# Override for one run with:
#   BENCH_UWS_WORKERS_PER_CPU=0.5 BENCH_FRAMEWORKS=uwebsockets bash script/benchmark.sh
BENCH_UWS_WORKERS_PER_CPU=${BENCH_UWS_WORKERS_PER_CPU:-0}
BENCH_UWS_LOOPS_PER_CPU=${BENCH_UWS_LOOPS_PER_CPU:-0}
for uws_thread_factor in "$BENCH_UWS_WORKERS_PER_CPU" "$BENCH_UWS_LOOPS_PER_CPU"; do
    case "$uws_thread_factor" in
        ''|*[!0-9.]*|*.*.*)
            echo "BENCH_UWS_*_PER_CPU must be a non-negative number, got: $uws_thread_factor" >&2
            return 1 ;;
    esac
done

# The order the report tables put their rows in. Both orders carry the same
# rows and the same numbers; only the order differs:
#
#   result     (default) best first, ranked by the number each benchmark
#              answers with: TPS in all three - for BenchRate the messages the
#              clients read back off the server per second - with EER breaking
#              a tie in BenchEcho and BenchRate (Connections samples no CPU, so
#              it has none). The rate test writes at a rate the clients set
#              rather than to completion, so what came back under that load is
#              its result there the way TPS is in the other two; Packet Sent is
#              the load rather than the answer
#   framework  the order FrameworkList in config/config.go lists them in,
#              which is by framework name, and which is also the order every
#              report was written in before this variable existed. It is what
#              puts a framework on the same row in every table and across runs,
#              whatever it scored, so two reports can be diffed. Only the Go
#              list reaches a report; the frameworks array below decides what
#              is built and run, and is kept in the same order so that the two
#              read alike
#
# In either order every ranked column - TPS, and EER - shows
# each row's share of the best in that column after it, the best being 100%,
# and carries [↓1] or [↓2] after its title for which key it is.
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

# What the run benchmarks: the Project row that heads the Summary table, so a
# report read on its own still says what it measured. Set it to name a run of
# something narrower, e.g. BENCH_PROJECT="uwebsockets threads, 3 CPUs".
# ${VAR-default} rather than ${VAR:-default}, so that an empty value is kept and
# leaves the row out. The report step alone takes it as a client flag:
#   bash script/report.sh -project="GO-WEBSOCKET-BENCHMARK nightly"
BENCH_PROJECT=${BENCH_PROJECT-GO-WEBSOCKET-BENCHMARK}

# The servers that take the -taskpool flags, in framework-name order like every
# other framework list here. The rest have no pool to swap and would exit on a
# flag they do not define.
#
# uwebsockets takes them too, but it is a C++ server, so none of the Go pools
# can run under it. It reads the value for what it says about where a server
# answers from: default and inline install no pool, which for uWS means the
# event loop, and every other mode hands the callback to a goroutine off the
# loop, which its own thread pool stands in for. Its Pool says which
# of the two it did, e.g. "nbio(pool)" or "default(loop)". It exits on a value
# that names no mode, as the Go servers do. See
# frameworks/uwebsockets/README.md.
#
# The benchmark clients ask /taskpool for the report's Pool of exactly these,
# from config.TaskPoolFrameworks in config/config.go, which is the same list;
# benchcli-uwscpp/test_scripts.py holds the two to each other.
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

# Which frameworks a run measures, and the order the servers are started and
# the clients run in. In framework-name order, like config.FrameworkList and
# taskpool_frameworks above, so that a framework is in the same place in every
# list and a new one has one obvious place to go.
frameworks=(
    "fasthttp"
    "fib"
    "fnet"
    "gobwas"
    "gorilla"
    "greatws"
    "greatws_event"
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
    "quickws"
    "tokio_tungstenite"
    "uwebsockets"
    "uws_events"
    "uws_std"
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
