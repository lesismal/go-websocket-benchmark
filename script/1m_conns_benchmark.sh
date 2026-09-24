#!/bin/bash

. ./script/env.sh || { return 1 2>/dev/null || exit 1; }

echo $line

. ./script/killall.sh

echo $line

. ./script/clean.sh

echo $line

# The subset this script measures, in framework-name order like every other
# framework list; see script/config.sh.
frameworks=(
    "fnet"
    "greatws"
    "greatws_event"
    "nbio_nonblocking"
    "uws_events"
)

print_env

echo $line

. ./script/build.sh || { return 1 2>/dev/null || exit 1; }

echo $line

# The servers and the benchmark client take different flags, and this script
# takes the client's. Forward only what a server actually defines: a run
# started with a client flag first used to hand it to every server as its
# -nodelay, and they all exited with "flag provided but not defined".
server_flags=""
for arg in "$@"; do
    case "$arg" in
        -nodelay=*|-reuseport=*|-b=*|-m=*) server_flags="${server_flags} ${arg}" ;;
    esac
done

if bench_runs_servers; then
    . ./script/servers.sh

    echo $line
fi

if ! bench_runs_clients; then
    echo "servers are up and left running. On the client node:"
    echo "  BENCH_ROLE=client BENCH_SERVER_HOST=<this host> bash script/1m_conns_benchmark.sh"
    echo "Stop them here afterwards with: bash script/killall.sh"
    echo $line
    return 0 2>/dev/null || exit 0
fi

sleep 3

# As in script/benchmark.sh: a failed client still leaves the others a report.
clients_failed=0
. ./script/clients.sh -c=1000000 -en=2000000 -b=1024 -rr=1 || clients_failed=1

# echo $line

# The report step reads BENCH_REPORT_SORT for the row order of its three
# tables: "result" (the default) ranks the best result first, "framework"
# keeps config.FrameworkList's order. Both carry the same rows and numbers, so
# script/report.sh alone re-reads a finished run the other way round. See
# script/config.sh.
. ./script/report.sh "$@"

echo $line

if [ "$clients_failed" -ne 0 ]; then
    echo "some benchmark clients failed; the report above covers the reports they wrote" >&2
    return 1 2>/dev/null || exit 1
fi
