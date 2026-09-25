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
    if [ "$f" = tokio_tungstenite ]; then
        # tokio_tungstenite takes none of the Go pool flags either: it answers
        # on its own logic thread pool. It ignores the flags it does not
        # define, so it gets this one and nothing of the Go pools'.
        taskpool_args="-logicpool=true"
    elif [ "$f" = uwebsockets ]; then
        # uwebsockets takes none of the Go pool flags: it answers from its
        # event loops, which it sizes against the CPUs it may run on with the
        # multiplier config.sh configures. Only that server defines these; the
        # Go ones would exit on a flag they do not have.
        taskpool_args="-logicpool=false -loopspercpu=${BENCH_UWS_LOOPS_PER_CPU}"
    else
        for tf in ${taskpool_frameworks[@]}; do
            if [ "$f" = "$tf" ]; then
                # An -inline entry is its framework's server run inline, which
                # is also what gives it its own ports: the server takes its
                # name from this flag (frameworks.Name). The framework's own
                # entry runs BENCH_TASKPOOL, which config.sh keeps off inline.
                pool=${BENCH_TASKPOOL}
                case "$f" in
                    *-inline) pool=inline ;;
                esac
                taskpool_args="-taskpool=${pool} -tpmin=${BENCH_TASKPOOL_MIN} -tpmax=${BENCH_TASKPOOL_MAX} -tpqueue=${BENCH_TASKPOOL_QUEUE}"
                break
            fi
        done
    fi
    ./script/server.sh $f $server_flags $taskpool_args
    # uwebsockets sizes its event loops itself, against the CPUs it was given,
    # so say what it built: the multiplier env.sh prints is what was asked
    # for, 0 for the server's own sizing.
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
