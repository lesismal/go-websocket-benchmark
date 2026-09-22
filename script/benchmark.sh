#!/bin/bash

. ./script/env.sh || { return 1 2>/dev/null || exit 1; }

echo $line

# Leftovers from an earlier run on this machine, whichever half it runs.
. ./script/killall.sh

echo $line

. ./script/clean.sh

echo $line

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
    echo "  BENCH_ROLE=client BENCH_SERVER_HOST=<this host> bash script/benchmark.sh"
    echo "Stop them here afterwards with: bash script/killall.sh"
    echo $line
    return 0 2>/dev/null || exit 0
fi

sleep 3

. ./script/clients.sh -rate=true "$@" || { return 1 2>/dev/null || exit 1; }

# echo $line

# The report step reads BENCH_REPORT_SORT for the row order of its three
# tables: "result" (the default) ranks the best result first, "framework"
# keeps config.FrameworkList's order. Both carry the same rows and numbers, so
# script/report.sh alone re-reads a finished run the other way round. See
# script/config.sh.
. ./script/report.sh "$@"

echo $line
