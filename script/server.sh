#!/bin/bash

. ./script/env.sh

framework=$1
shift

echo "run ${framework} server on cpu ${server_cpu_list:-unbound}"
# "$@" rather than $2 through $9: the taskpool flags alone are four of them.
nohup $limit_cpu_server "./output/bin/${framework}.server" "$@" \
    >"./output/log/${preffix}${framework}${suffix}.log" 2>&1 &
# nohup and taskset both exec the server, so this is the server's own pid:
# script/serverctl.sh watches it to tell a server that is still starting from
# one that has exited, and waits on it to be gone after its turn.
echo $! >"./output/log/${preffix}${framework}${suffix}.pid"
