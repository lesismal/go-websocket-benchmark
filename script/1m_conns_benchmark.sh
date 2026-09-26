#!/bin/bash

# BENCH_FRAMEWORKS picks from this script's list below rather than from
# script/config.sh's, which may have these commented out: taken here and
# unset, so that config.sh - sourced again by every script this one runs -
# never checks it against its own list.
million_selected=${BENCH_FRAMEWORKS:-}
unset BENCH_FRAMEWORKS

. ./script/env.sh || { return 1 2>/dev/null || exit 1; }

# The subset this script measures, in framework-name order like every other
# framework list; see script/config.sh. Checked here, before anything is
# killed or cleaned, and made the run's frameworks once that is done:
# script/killall.sh sources config.sh again, which resets them to its own.
million_frameworks=(
    "fib"
    "fib-inline"
    "fnet"
    "fnet-inline"
    "greatws"
    "greatws-inline"
    "greatws_event"
    "greatws_event-inline"
    "nbio_nonblocking"
    "uws_events"
)

# Optional comma-separated subset of the list above, in the order given, e.g.
#   BENCH_FRAMEWORKS=fib,fib-inline bash script/1m_conns_benchmark.sh
if [ -n "$million_selected" ]; then
    million_all=("${million_frameworks[@]}")
    IFS=',' read -r -a million_requested <<< "$million_selected"
    million_frameworks=()
    for million_name in "${million_requested[@]}"; do
        million_found=false
        for million_known in "${million_all[@]}"; do
            if [ "$million_name" = "$million_known" ]; then
                million_found=true
                break
            fi
        done
        if [ "$million_found" != true ]; then
            echo "Unsupported framework in BENCH_FRAMEWORKS: $million_name (this script runs: ${million_all[*]})" >&2
            return 1 2>/dev/null || exit 1
        fi
        million_frameworks+=("$million_name")
    done
    if [ "${#million_frameworks[@]}" -eq 0 ]; then
        echo "BENCH_FRAMEWORKS must select at least one framework" >&2
        return 1 2>/dev/null || exit 1
    fi
fi

echo $line

. ./script/killall.sh

echo $line

. ./script/clean.sh

echo $line

frameworks=("${million_frameworks[@]}")

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

# As in script/benchmark.sh: only a server node starts them all up front.
if bench_runs_servers && ! bench_runs_clients; then
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

# As in script/benchmark.sh: a failed client still leaves the others a report.
# The flags this script was given go after its own, so one given on the
# command line wins - both clients take the last value of a repeated flag -
# e.g. a smaller -c where the machine cannot hold a million connections.
clients_failed=0
. ./script/clients.sh -c=1000000 -en=2000000 -b=1024 -rr=1 "$@" || clients_failed=1

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
