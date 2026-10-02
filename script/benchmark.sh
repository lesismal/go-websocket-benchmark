#!/bin/bash

# -socketsyscalls[=BOOL] and -eventloops=N are the drivers' own flags, for
# BENCH_FIB_SOCKET_SYSCALLS and BENCH_EVENTLOOPS in script/config.sh: taken
# out here, before env.sh checks the values, so that neither the clients nor
# the report step is handed a flag it does not define.
driver_args=()
for arg in "$@"; do
    case "$arg" in
        -socketsyscalls|--socketsyscalls) BENCH_FIB_SOCKET_SYSCALLS=true ;;
        -socketsyscalls=*|--socketsyscalls=*) BENCH_FIB_SOCKET_SYSCALLS=${arg#*=} ;;
        -eventloops=*|--eventloops=*) BENCH_EVENTLOOPS=${arg#*=} ;;
        *) driver_args+=("$arg") ;;
    esac
done
set -- ${driver_args[@]+"${driver_args[@]}"}

. ./script/env.sh || { return 1 2>/dev/null || exit 1; }

echo $line

# Leftovers from an earlier run on this machine, whichever half it runs.
. ./script/killall.sh

echo $line

. ./script/clean.sh

echo $line

# After killall.sh, so that leftover servers' connections are not counted; the
# clients' own -c default is 10000.
bench_check_file_max 10000 "$@" || { return 1 2>/dev/null || exit 1; }

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

# A server node starts every server here and leaves them up for the client
# node. A single-node run starts each one just before its turn instead, in
# script/clients.sh, so that only the server being measured is ever up.
if bench_runs_servers && ! bench_runs_clients; then
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

# A framework whose client failed must not cost the others their report: the
# report step reads whatever JSON the run did write, so it runs either way, and
# the failure is returned after it.
clients_failed=0
. ./script/clients.sh -rate=true "$@" || clients_failed=1

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
