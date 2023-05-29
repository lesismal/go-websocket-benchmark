#!/bin/bash

# payloadSize=1024
# clientNum=10000

# if [ $1 ] >0; then
#     clientNum=$1
# fi

frameworks=(
    "gobwas"
    "gorilla"
    "gws"
    "gws_basedon_stdhttp"
    "nbio_basedon_stdhttp"
    "nbio_mod_blocking"
    "nbio_mod_mixed"
    "nbio_mod_nonblocking"
    "nhooyr"
)

clean() {
    rm -rf ./output
    for f in ${frameworks[@]}; do
        killall -9 "${f}.server" 1>/dev/null 2>&1
    done
}

clean

mkdir -p ./output/bin
mkdir -p ./output/log

# build
echo "building..."
for f in ${frameworks[@]}; do
    go build -o "./output/bin/${f}.server" "./frameworks/${f}"
done
go build -o "./output/bin/bench.client" "./client"
echo "build done"

# run
total_cpu_num=$(getconf _NPROCESSORS_ONLN)
want_cpu_num=$((total_cpu_num >= 16 ? 7 : total_cpu_num / 2 - 1))
limit_cpu="taskset -c 0-$want_cpu_num"
echo "run each server on cpu 0-$want_cpu_num"
# start all servers together, else it would hard to bind addr and start failed after some benchmark
for f in ${frameworks[@]}; do
    nohup $taskset_server "./output/bin/${f}.server" >"./output/log/${f}.log" 2>&1 &
done
for f in ${frameworks[@]}; do
    ./output/bin/bench.client -f="${f}" -c=50000 -n=2000000
done


# clean
