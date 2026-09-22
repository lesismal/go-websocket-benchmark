#!/bin/bash

# . ./script/env.sh
# . ./script/config.sh

# Flags for every server, set by the driver that sources this: the ones the
# servers define, such as -nodelay, with the benchmark client's own filtered
# out. Read from a variable rather than from "$@" because `source file` with no
# arguments leaves the caller's positional parameters in place, which is how
# the client's flags used to reach the servers.
if [ -z "${server_flags+set}" ]; then
    # Invoked directly rather than sourced: take our own arguments.
    server_flags="$*"
fi

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
    ./script/server.sh $f $server_flags $taskpool_args
done
