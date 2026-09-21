#!/bin/bash

. ./script/env.sh || { return 1 2>/dev/null || exit 1; }

# echo "run client on cpu ${client_cpu_num}-$((total_cpu_num - 1))"
$limit_cpu_client ./output/bin/bench.client "$@"
