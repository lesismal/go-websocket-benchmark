#!/bin/bash

. ./script/env.sh

framework=$1
shift

echo "run ${framework} server on cpu ${server_cpu_list:-unbound}"
# "$@" rather than $2 through $9: the taskpool flags alone are four of them.
nohup $limit_cpu_server "./output/bin/${framework}.server" "$@" \
    >"./output/log/${preffix}${framework}${suffix}.log" 2>&1 &
