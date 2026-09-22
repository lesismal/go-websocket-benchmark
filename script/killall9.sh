#!/bin/bash

. ./script/env.sh

echo "kill all ..."

killcmd=pkill
if [ $(which killall) ]; then
    killcmd=killall
fi

# run
# Same rule as killall.sh: the servers are ours to stop only when we started
# them, or when this is the server node being cleared.
if declare -F bench_owns_servers >/dev/null && ! bench_owns_servers && bench_runs_clients; then
    echo "skip the servers: they are on ${BENCH_SERVER_HOST} and not ours to stop"
else
    for f in ${frameworks[@]}; do
        $killcmd "${f}.server"
    done
fi
$killcmd "bench.client"

echo "kill all done"
