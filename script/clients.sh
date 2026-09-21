#!/bin/bash

# . ./script/env.sh

clients_status=0
for f in ${frameworks[@]}; do
    echo
    # echo "start bench ${f}" "$@"
    echo "run client to ${f}, on cpu ${client_cpu_list:-unbound}"
    . ./script/client.sh -f=$f "$@" || clients_status=$?
    . ./script/killone.sh "${f}.server"
    for ((i = 1; i <= $SleepTime; i++)); do
        echo "sleep $i ..."
        sleep 1
    done
done

. ./script/killall9.sh

return "$clients_status" 2>/dev/null || exit "$clients_status"
