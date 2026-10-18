#!/bin/bash

. ./script_e/config.sh

# Connections=(10000 50000 100000)
# BodySize=(128 512 1024 4096)
# BenchTime=(5000000)

# -socketsyscalls[=BOOL] and -eventloops=N are the drivers' own flags, for
# BENCH_FIB_SOCKET_SYSCALLS and BENCH_EVENTLOOPS in script_e/config.sh: taken
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

. ./script_e/env.sh || { return 1 2>/dev/null || exit 1; }

echo $line
. ./script_e/clean.sh

echo $line

print_env

echo $line

. ./script_e/build.sh || { return 1 2>/dev/null || exit 1; }

echo $line

. ./script_e/killall.sh
sleep 1
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

# As in script_e/benchmark.sh: only a server node starts them all up front; a
# single-node run starts each one just before its turn, below.
if bench_runs_servers && ! bench_runs_clients; then
    . ./script_e/servers.sh
fi
echo $line

if ! bench_runs_clients; then
    echo "servers are up and left running. On the client node:"
    echo "  BENCH_ROLE=client BENCH_SERVER_HOST=<this host> bash script_e/benchmarkN.sh"
    echo "Stop them here afterwards with: bash script_e/killall.sh"
    return 0 2>/dev/null || exit 0
fi

. ./script_e/serverctl.sh || { return 1 2>/dev/null || exit 1; }

# The server settings of this run, for the report's Summary; see
# bench_write_server_parameters in script_e/env.sh.
bench_write_server_parameters

# As in script_e/benchmark.sh: a failed client still leaves the others a report.
clients_failed=0
if bench_owns_servers; then
    bench_check_reserved_ports
fi
bench_pause_needed=false
for f in ${frameworks[@]}; do
    if bench_owns_servers; then
        echo
        bench_pause
        if ! bench_start_server "$f"; then
            echo "skip the clients to ${f}: its server did not start" >&2
            clients_failed=1
            bench_pause_needed=true
            continue
        fi
        # Up is not settled: give it a moment before the connections arrive.
        sleep $ServerReadyDelay
    fi
    for c in ${Connections[@]}; do
        for b in ${BodySize[@]}; do
            for n in ${BenchTime[@]}; do
                # echo $line
                bench_pause
                suffix="_${c}_${b}_${n}"
                #echo "benchmarkN: [${f}], ${c} connections, ${b} payload, ${n} times"
                # BenchEcho, then BenchPipeline on a restarted server; see
                # bench_run_client in script_e/serverctl.sh.
                bench_run_client "$f" -c=$c -b=$b -en=$n -suffix=${suffix} -rate=true || clients_failed=1
                bench_pause_needed=true
            done
        done
    done
    if bench_owns_servers; then
        bench_stop_server "$f"
    fi
done

# The report step reads BENCH_REPORT_SORT for the row order of its three
# tables: "result" (the default) ranks the best result first, "framework"
# keeps config.FrameworkList's order. Both carry the same rows and numbers, so
# script_e/report.sh alone re-reads a finished run the other way round. See
# script_e/config.sh.
for c in ${Connections[@]}; do
    for b in ${BodySize[@]}; do
        for n in ${BenchTime[@]}; do
            # echo $line
            suffix="_${c}_${b}_${n}"
            . ./script_e/report.sh -suffix=${suffix} "$@"
        done
    done
done
# echo $line

if [ "$clients_failed" -ne 0 ]; then
    echo "some benchmark clients failed; the report above covers the reports they wrote" >&2
    return 1 2>/dev/null || exit 1
fi
