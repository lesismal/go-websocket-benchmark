#!/bin/bash

. ./script/env.sh

# echo "run client on cpu ${client_cpu_num}-$((total_cpu_num - 1))"
$limit_cpu_client ./output/bin/bench.client $1 $2 $3 $4 $5 $6 $7 $8 $9
