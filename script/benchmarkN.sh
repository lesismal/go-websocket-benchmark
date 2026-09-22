#!/bin/bash

. ./script/config.sh

# Connections=(10000 50000 100000)
# BodySize=(128 512 1024 4096)
# BenchTime=(2000000)

. ./script/env.sh || { return 1 2>/dev/null || exit 1; }

echo $line
. ./script/clean.sh

echo $line

print_env

echo $line

. ./script/build.sh || { return 1 2>/dev/null || exit 1; }

echo $line

. ./script/killall.sh
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

if bench_runs_servers; then
    . ./script/servers.sh
    sleep 3
fi
echo $line

if ! bench_runs_clients; then
    echo "servers are up and left running. On the client node:"
    echo "  BENCH_ROLE=client BENCH_SERVER_HOST=<this host> bash script/benchmarkN.sh"
    echo "Stop them here afterwards with: bash script/killall.sh"
    return 0 2>/dev/null || exit 0
fi

for f in ${frameworks[@]}; do
    echo "run ${f} server on cpu ${server_cpu_list:-unbound}"
    # nohup $limit_cpu_server "./output/bin/${f}.server" -b=$b >"./output/log/${f}${suffix}.log" 2>&1 &
    for c in ${Connections[@]}; do
        for b in ${BodySize[@]}; do
            for n in ${BenchTime[@]}; do
                # echo $line
                suffix="_${c}_${b}_${n}"
                #echo "benchmarkN: [${f}], ${c} connections, ${b} payload, ${n} times"
                . ./script/client.sh -f=$f -ip=${BENCH_SERVER_HOST} -c=$c -b=$b -en=$n -suffix=${suffix} -rate=true || { return 1 2>/dev/null || exit 1; }
                sleep $SleepTime
            done
        done
    done
    if bench_owns_servers; then
        . ./script/killone.sh "${f}.server"
    fi
done

for c in ${Connections[@]}; do
    for b in ${BodySize[@]}; do
        for n in ${BenchTime[@]}; do
            # echo $line
            suffix="_${c}_${b}_${n}"
            . ./script/report.sh -suffix=${suffix} "$@"
        done
    done
done
# echo $line
