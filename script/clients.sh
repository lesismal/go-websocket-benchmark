#!/bin/bash

# . ./script/env.sh

. ./script/serverctl.sh || { return 1 2>/dev/null || exit 1; }

clients_status=0

if ! bench_owns_servers; then
    echo "servers are on ${BENCH_SERVER_HOST} and stay up for the whole run;"
    echo "stop them there with script/killall.sh when it is done"
fi

# One framework at a time: its server is started just before its turn and
# stopped right after it, so no other server is up while it is measured.
if bench_owns_servers; then
    bench_check_reserved_ports
fi
bench_pause_needed=false
for f in ${frameworks[@]}; do
    echo
    bench_pause
    # Only the machine that started a server can stop it, and only it should:
    # killing by process name on a shared client node would reach whatever
    # else is running there.
    if bench_owns_servers; then
        if ! bench_start_server "$f"; then
            echo "skip the client to ${f}: its server did not start" >&2
            clients_status=1
            bench_pause_needed=true
            continue
        fi
        # Up is not settled: give it a moment before the connections arrive.
        sleep $ServerReadyDelay
    fi
    # echo "start bench ${f}" "$@"
    echo "run client to ${f} at ${BENCH_SERVER_HOST}, on cpu ${client_cpu_list:-unbound}"
    # -ip first, so a host given on the command line still wins: both clients
    # take the last value of a repeated flag.
    . ./script/client.sh -f=$f -ip=${BENCH_SERVER_HOST} "$@" || clients_status=$?
    if bench_owns_servers; then
        bench_stop_server "$f"
    fi
    bench_pause_needed=true
done

if bench_owns_servers; then
    . ./script/killall9.sh
fi

return "$clients_status" 2>/dev/null || exit "$clients_status"
