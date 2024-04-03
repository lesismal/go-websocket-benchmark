#!/bin/bash

# . ./script/env.sh

for f in ${frameworks[@]}; do
    echo
    # echo "start bench ${f}" $1 $2 $3 $4 $5 $6 $7 $8 $9
    echo "run client to ${f}, on cpu ${client_cpu_num}-$((total_cpu_num - 1))"
    . ./script/client.sh -f=$f $1 $2 $3 $4 $5 $6 $7 $8 $9
    . ./script/killone.sh "${f}.server"
    for ((i = 1; i <= $SleepTime; i++)); do
        echo "sleep $i ..."
        sleep 1
    done
done

. ./script/killall9.sh
