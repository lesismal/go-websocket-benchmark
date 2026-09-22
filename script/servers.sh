#!/bin/bash

# . ./script/env.sh
# . ./script/config.sh

# start all servers together, else it would hard to bind addr and start failed after some benchmark
for f in ${frameworks[@]}; do
    echo
    # Only the servers that define the flags may be given them.
    taskpool_args=""
    for tf in ${taskpool_frameworks[@]}; do
        if [ "$f" = "$tf" ]; then
            taskpool_args="-taskpool=${BENCH_TASKPOOL} -tpmin=${BENCH_TASKPOOL_MIN} -tpmax=${BENCH_TASKPOOL_MAX} -tpqueue=${BENCH_TASKPOOL_QUEUE}"
            break
        fi
    done
    # $1 nodelay
    ./script/server.sh $f $1 $taskpool_args
done
