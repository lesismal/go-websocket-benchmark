#!/bin/bash

# . ./script/env.sh

clients_status=0

if ! bench_owns_servers; then
    echo "servers are on ${BENCH_SERVER_HOST} and stay up for the whole run;"
    echo "stop them there with script/killall.sh when it is done"
fi

for f in ${frameworks[@]}; do
    echo
    # echo "start bench ${f}" "$@"
    echo "run client to ${f} at ${BENCH_SERVER_HOST}, on cpu ${client_cpu_list:-unbound}"
    # -ip first, so a host given on the command line still wins: both clients
    # take the last value of a repeated flag.
    . ./script/client.sh -f=$f -ip=${BENCH_SERVER_HOST} "$@" || clients_status=$?
    # Only the machine that started a server can stop it, and only it should:
    # killing by process name on a shared client node would reach whatever
    # else is running there.
    if bench_owns_servers; then
        . ./script/killone.sh "${f}.server"
    fi
    for ((i = 1; i <= $SleepTime; i++)); do
        echo "sleep $i ..."
        sleep 1
    done
done

if bench_owns_servers; then
    . ./script/killall9.sh
fi

return "$clients_status" 2>/dev/null || exit "$clients_status"
