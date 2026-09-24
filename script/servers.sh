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
    # uwebsockets sizes its threads against the CPUs it may run on, and takes
    # the two multipliers config.sh configures that with. Only that server
    # defines them; the Go ones would exit on a flag they do not have.
    if [ "$f" = uwebsockets ]; then
        taskpool_args="${taskpool_args} -tpmaxpercpu=${BENCH_UWS_WORKERS_PER_CPU} -loopspercpu=${BENCH_UWS_LOOPS_PER_CPU}"
    fi
    ./script/server.sh $f $server_flags $taskpool_args
    # uwebsockets sizes its event loops and its task pool itself, against the
    # CPUs it was given, so say what it built: the multipliers env.sh prints
    # are what was asked for, 0 for the server's own sizing.
    if [ "$f" = uwebsockets ]; then
        uws_log="./output/log/${preffix}${f}${suffix}.log"
        uws_threads=""
        for ((i = 0; i < 50; i++)); do
            uws_threads=$(grep -m1 "^uwebsockets threads:" "$uws_log" 2>/dev/null) && break
            sleep 0.1
        done
        echo "${uws_threads:-uwebsockets threads: not logged yet, see $uws_log}"
    fi
done
