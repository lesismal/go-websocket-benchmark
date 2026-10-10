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

# Goroutine pool the servers run their callbacks on. Every value it takes,
# and what it selects:
#
#   default       each framework's own scheduling. Not a pool, and not what a
#                 run without this variable measures. fio answers in its
#                 poller under this one value; the rest run the pool they
#                 ship with. fio is given it whatever this variable
#                 holds, since its entry is that arrangement; see
#                 bench_server_args in script/serverctl.sh
#   inline        no pool: the callback runs on the I/O goroutine that read
#                 the frame, so the answer is written from the event loop.
#                 fnet and uws_events refuse it: their executors must not
#                 run a connection's task inline
#   go            one goroutine per task, bounded by nothing
#   fib_adaptive  github.com/lesismal/fib/taskpool in adaptive mode, which
#                 is fib's own default and this benchmark's
#   fib_elastic   the same pool in elastic mode: goroutines forked on demand
#                 up to a ceiling
#   nbio          github.com/lesismal/nbio/taskpool
#   fnet          github.com/linfeip/fnet/taskpool, sharded: one lock-free
#                 queue per shard, workers started on demand and then kept
#   fio           fio's stream2 business pool
#   uws           github.com/limpo1989/taskgo, the pool UIO runs uws's
#                 connections on, set up as UIO sets it up; it refuses work
#                 only once BENCH_TASKPOOL_QUEUE bounds its pending tasks
#
# default and inline install no pool; all the rest answer off the event loop.
# None of this reaches the uwebsockets server, which echoes from its event
# loops; see BENCH_UWS_LOOPS_PER_CPU below.
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

# uwebsockets only: how many uWS event loops its C++ server builds, one thread
# each, which do the poll, the read, the frame parse, the echo and the write.
# A multiplier of the CPUs the server may actually run on (sched_getaffinity,
# so the taskset mask script/env.sh pins it with counts, not the whole host).
# N here rather than a thread count so that one setting means the same
# arrangement on a 4-core laptop and a 64-core server: the count is
# round(N * cpus), and a multiplier that rounds to nothing still gets one
# thread. 0 (the default) leaves the server its own sizing: one loop per CPU.
#
# Override for one run with:
#   BENCH_UWS_LOOPS_PER_CPU=0.5 BENCH_FRAMEWORKS=uwebsockets bash script/benchmark.sh
BENCH_UWS_LOOPS_PER_CPU=${BENCH_UWS_LOOPS_PER_CPU:-0}
for uws_thread_factor in "$BENCH_UWS_LOOPS_PER_CPU"; do
    case "$uws_thread_factor" in
        ''|*[!0-9.]*|*.*.*)
            echo "BENCH_UWS_*_PER_CPU must be a non-negative number, got: $uws_thread_factor" >&2
            return 1 ;;
    esac
done

# How many event loops each server in eventloop_frameworks below runs: the
# goroutines (threads, for tokio_tungstenite and uwebsockets) that wait on the
# poller. 0 (the default) leaves every framework its own default count, which
# differs between them; N > 0 gives every one of them the same N. Each takes
# it through its own knob, which script/server.sh passes it as:
#
#   fib                 -eventloops  fib.Config.IOPollerCount (default: CPUs/4, at least 1)
#   fnet                -eventloops  fnet.Options.NumLoops (default: GOMAXPROCS/4, at least 2)
#   fio(_event)         -eventloops  fio.WithEventLoops (default: one per CPU)
#   hertz               -eventloops  netpoll.SetNumLoops (default: GOMAXPROCS/20+1)
#   nbio_mixed          -eventloops  nbhttp.Config.NPoller (default: one per CPU)
#   nbio_nonblocking    -eventloops  nbhttp.Config.NPoller (default: one per CPU)
#   tokio_tungstenite   -threads     its current-thread runtimes (default: one per CPU)
#   uwebsockets         -loops       its uWS::App threads (default: one per CPU, or
#                                    BENCH_UWS_LOOPS_PER_CPU, which -loops overrides)
#   uws_events          -eventloops  uio.Events.Pollers (default: 4, capped by the CPUs)
#
# "CPU" there is the CPUs the server may run on, after script/env.sh pins it.
# Exported, since script/server.sh runs as a process of its own.
#
# Override for one run with: BENCH_EVENTLOOPS=4 bash script/benchmark.sh
# or with the drivers' own flag, which they take out of their arguments before
# the clients see them: bash script/benchmark.sh -eventloops=4
BENCH_EVENTLOOPS=${BENCH_EVENTLOOPS:-0}
case "$BENCH_EVENTLOOPS" in
    ''|*[!0-9]*)
        echo "Unsupported BENCH_EVENTLOOPS: $BENCH_EVENTLOOPS (want a non-negative integer, 0 for each framework's own default)" >&2
        return 1 ;;
esac
# A leading zero would be octal to the shell's arithmetic and a different
# number to the servers' parsers.
BENCH_EVENTLOOPS=$((10#$BENCH_EVENTLOOPS))
export BENCH_EVENTLOOPS

# The order the report tables put their rows in. Both orders carry the same
# rows and the same numbers; only the order differs:
#
#   result     (default) best first, ranked by the number each benchmark
#              answers with: TPS in all three - for BenchPipeline the messages the
#              clients read back off the server per second - with CPU EER
#              breaking a tie in BenchEcho and BenchPipeline, and MEM EER a tie
#              on both (Connections samples neither, so it has none). The rate test writes at a rate the clients set
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
# In either order every ranked column - TPS, CPU EER and MEM EER - shows
# each row's share of the best in that column after it, the best being 100%,
# and carries [↓1], [↓2] or [↓3] after its title for which key it is.
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

# fib only: the calls its server reads and writes its sockets with, which is
# fib.Config.SocketSyscalls:
#
#   true   (default, as in fib) recvfrom, sendto and sendmsg
#   false  read, write and writev, which reach the same socket code through
#          the VFS, and with it the security module's file permission hook on
#          every call (AppArmor's, in a Docker container)
#
# Linux only: elsewhere fib ignores it.
# script/server.sh gives it to the fib server as -socketsyscalls,
# and to no other, which would exit on a flag it does not define. Exported,
# since script/server.sh runs as a process of its own.
#
# Override for one run with: BENCH_FIB_SOCKET_SYSCALLS=false bash script/benchmark.sh
# or with the drivers' own flag, which they take out of their arguments before
# the clients see them: bash script/benchmark.sh -socketsyscalls=false
BENCH_FIB_SOCKET_SYSCALLS=${BENCH_FIB_SOCKET_SYSCALLS:-true}
case "$BENCH_FIB_SOCKET_SYSCALLS" in
    true|false) ;;
    *) echo "Unsupported BENCH_FIB_SOCKET_SYSCALLS: $BENCH_FIB_SOCKET_SYSCALLS (want true or false)" >&2; return 1 ;;
esac
export BENCH_FIB_SOCKET_SYSCALLS

# What the run benchmarks: the Project row that heads the Summary table, so a
# report read on its own still says what it measured. Set it to name a run of
# something narrower, e.g. BENCH_PROJECT="uwebsockets threads, 3 CPUs".
# ${VAR-default} rather than ${VAR:-default}, so that an empty value is kept and
# leaves the row out. The report step alone takes it as a client flag:
#   bash script/report.sh -project="GO-WEBSOCKET-BENCHMARK nightly"
BENCH_PROJECT=${BENCH_PROJECT-GO-WEBSOCKET-BENCHMARK}

# The servers that take the -taskpool flags, in framework-name order like every
# other framework list here. The rest have no pool to swap and would exit on a
# flag they do not define. uws_std is the uws_events program built for UIO's
# stdio backend, which has no executor: it accepts the flags but installs no
# pool, so it is not passed them.
#
# tokio_tungstenite and uwebsockets are listed for their /taskpool route only:
# they are Rust and C++ servers, so none of the Go pools can run under them,
# and script/servers.sh passes them their own flags instead of the -taskpool
# ones (-logicpool, on for tokio_tungstenite and off for uwebsockets, and for
# uwebsockets BENCH_UWS_LOOPS_PER_CPU), so BENCH_TASKPOOL* never changes what
# they run. Their Pool is "logicpool" and "inline". See
# frameworks/tokio_tungstenite/README.md and frameworks/uwebsockets/README.md.
#
# The benchmark clients ask /taskpool for the report's Pool of exactly these,
# from config.TaskPoolFrameworks in config/config.go, which is the same list;
# benchcli-uwscpp/test_scripts.py holds the two to each other.
taskpool_frameworks=(
    "fib"
    "fnet"
    "nbio_mixed"
    "nbio_nonblocking"
    "tokio_tungstenite"
    "uwebsockets"
    "uws_events"
)

# The servers that run event loops of their own and take BENCH_EVENTLOOPS
# above, in framework-name order like every other framework list here. The
# rest serve each connection on goroutines of the Go runtime's netpoller (or,
# for nbio_blocking, nbio_std, hertz_std and uws_std, a backend that reads on
# goroutines), so there is no loop count to set, and the Go ones would exit on
# a flag they do not define.
eventloop_frameworks=(
    "fib"
    "fio"
    "fnet"
    "hertz"
    "nbio_mixed"
    "nbio_nonblocking"
    "tokio_tungstenite"
    "uwebsockets"
    "uws_events"
)

Connections=(5000 50000)
BodySize=(512 1024)
BenchTime=(2000000)
# Seconds of quiet between two measurements; none after the last one.
SleepTime=5

# A single-node run starts each framework's server just before its turn and
# stops it right after, so only the server being measured is ever up; see
# script/serverctl.sh. The client starts ServerReadyDelay seconds after the
# server is listening on all of its ports. A server that is not listening
# within BENCH_SERVER_START_TIMEOUT seconds counts as failed to start, and one
# still running BENCH_SERVER_STOP_TIMEOUT seconds after its SIGINT is killed.
# One that fails is tried twice more, BENCH_SERVER_RETRY_DELAY seconds apart.
#
# Starting servers one at a time needs their ports kept out of the ephemeral
# range, or a client's TIME_WAIT sockets can hold the next server's ports;
# script/docker_benchmark.sh does this itself, and a Linux host is warned with
# the sysctl that does it. See bench_server_reserved_ports in script/ports.sh.
ServerReadyDelay=1
BENCH_SERVER_START_TIMEOUT=${BENCH_SERVER_START_TIMEOUT:-60}
BENCH_SERVER_STOP_TIMEOUT=${BENCH_SERVER_STOP_TIMEOUT:-30}
BENCH_SERVER_RETRY_DELAY=${BENCH_SERVER_RETRY_DELAY:-30}

# Which frameworks a run measures, and the order the servers are started and
# the clients run in. In framework-name order, like config.FrameworkList and
# taskpool_frameworks above, so that a framework is in the same place in every
# list and a new one has one obvious place to go.
frameworks=(
    "fasthttp"
    "fib"
    "fio"
    "fnet"
    "gobwas"
    "gorilla"
    "gws"
    "gws_std"
    "hertz"
    # "hertz_std"
    # "nbio_blocking"
    # "nbio_mixed"
    "nbio_nonblocking"
    # "nbio_std"
    "nettyws"
    "nhooyr"
    "quickws"
    "tokio_tungstenite"
    # "uwebsockets"
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
