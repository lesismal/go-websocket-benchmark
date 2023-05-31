#!/bin/bash

. ./script/env.sh

echo "run ${1} server on cpu 0-${server_cpu_num}"
nohup $limit_cpu_server "./output/bin/${1}.server" $2 $3 $4 $5 $6 $7 $8 $9 >"./output/log/${preffix}${1}${suffix}.log" 2>&1 &
