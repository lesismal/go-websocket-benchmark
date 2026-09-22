#!/bin/bash

. ./script/env.sh

echo "kill all ..."

# run
# Servers only when they are ours to stop. A client node has none of ours, and
# killing by name on the machine whose servers are already up - the way to run
# the clients again without restarting them - would stop the run instead of
# clearing leftovers. Use this script on the server node to stop them.
if declare -F bench_owns_servers >/dev/null && ! bench_owns_servers && bench_runs_clients; then
    echo "skip the servers: they are on ${BENCH_SERVER_HOST} and not ours to stop"
else
    for f in ${frameworks[@]}; do
        . ./script/killone.sh "${f}.server"
    done
fi
. ./script/killone.sh "bench.client"

echo "kill all done"
