#!/bin/bash

. ./script/util.sh

echo "run each server on cpu 0-$want_cpu_num"
# start all servers together, else it would hard to bind addr and start failed after some benchmark
for f in ${frameworks[@]}; do
    echo
    echo "start ${f}.server"
    ./script/server.sh $f
done

