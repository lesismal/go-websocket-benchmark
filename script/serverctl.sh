#!/bin/bash

# Starting and stopping one framework's server, for the drivers that measure
# one framework at a time: each server is started just before its turn and
# stopped right after it, so that no idle server holds memory, CPU or ports
# while another one is measured. Sourced; defines functions only.
#
# Sourced from a caller that already has env.sh, or on its own.
if ! declare -F bench_owns_servers >/dev/null; then
    . ./script/env.sh || { return 1 2>/dev/null || exit 1; }
fi

# The server's flags besides the ones every server takes: only the servers
# that define the pool flags may be given them.
bench_server_args() {
    local f=$1 tf
    if [ "$f" = tokio_tungstenite ]; then
        # tokio_tungstenite takes none of the Go pool flags either: it answers
        # on its own logic thread pool. It ignores the flags it does not
        # define, so it gets this one and nothing of the Go pools'.
        echo "-logicpool=true"
        return
    fi
    if [ "$f" = uwebsockets ]; then
        # uwebsockets takes none of the Go pool flags: it answers from its
        # event loops, which it sizes against the CPUs it may run on with the
        # multiplier config.sh configures. Only that server defines these; the
        # Go ones would exit on a flag they do not have.
        echo "-logicpool=false -loopspercpu=${BENCH_UWS_LOOPS_PER_CPU}"
        return
    fi
    for tf in "${taskpool_frameworks[@]}"; do
        if [ "$f" = "$tf" ]; then
            echo "-taskpool=${BENCH_TASKPOOL} -tpmin=${BENCH_TASKPOOL_MIN} -tpmax=${BENCH_TASKPOOL_MAX} -tpqueue=${BENCH_TASKPOOL_QUEUE}"
            return
        fi
    done
}

# Where script/server.sh puts them. It is run with preffix and suffix empty,
# which a driver sets for the client's reports rather than for the server.
bench_server_log() { echo "./output/log/${1}.log"; }
bench_server_pidfile() { echo "./output/log/${1}.pid"; }

. ./script/ports.sh

# Warns, once, when the kernel may hand a server's ports out as ephemeral
# ones: see bench_server_reserved_ports in script/ports.sh. Linux only; the
# fix is a sysctl, which is the operator's to set, so this only says which.
bench_check_reserved_ports() {
    local range_file=/proc/sys/net/ipv4/ip_local_port_range
    local reserved_file=/proc/sys/net/ipv4/ip_local_reserved_ports
    local low high unreserved
    [ -r "$range_file" ] && [ -r "$reserved_file" ] || return 0
    read -r low high <"$range_file"
    unreserved=$(bench_server_reserved_ports | tr ',' '\n' | awk -F- \
        -v low="$low" -v high="$high" -v reserved="$(cat "$reserved_file")" '
        BEGIN { n = split(reserved, r, ",") }
        {
            for (p = $1; p <= $2; p++) {
                if (p < low || p > high) continue
                covered = 0
                for (i = 1; i <= n; i++) {
                    split(r[i], b, "-"); if (b[2] == "") b[2] = b[1]
                    if (p >= b[1] && p <= b[2]) { covered = 1; break }
                }
                if (!covered) { print p; exit }
            }
        }' | head -n 1)
    if [ -n "$unreserved" ]; then
        echo "warning: server port ${unreserved} is in the ephemeral range ${low}-${high} and not reserved:" >&2
        echo "  a server may fail to bind a port the client before it left in TIME_WAIT. Reserve them with" >&2
        echo "  sysctl -w net.ipv4.ip_local_reserved_ports=$(bench_server_reserved_ports)" >&2
    fi
}

# The TCP ports something listens on here, one per line, or nothing when
# neither tool is installed and the caller has to connect instead. Reading the
# socket table does not touch the server, where a probe connection would.
bench_listen_tool=""
if command -v ss >/dev/null 2>&1; then
    bench_listen_tool=ss
elif command -v lsof >/dev/null 2>&1; then
    bench_listen_tool=lsof
fi
bench_listening_ports() {
    case "$bench_listen_tool" in
        ss) ss -ltn 2>/dev/null | awk 'NR > 1 { a = $4; sub(/.*:/, "", a); print a }' ;;
        lsof) lsof -nP -iTCP -sTCP:LISTEN 2>/dev/null | awk 'NR > 1 { a = $9; sub(/.*:/, "", a); print a }' ;;
    esac
}

# Waits until the server is listening on every benchmark port, or fails when it
# exits or BENCH_SERVER_START_TIMEOUT runs out first.
bench_wait_server_ready() {
    local f=$1 pid=$2 range min max p deadline
    local -a pending still
    range=$(bench_server_port_range "$f")
    if [ -z "$range" ]; then
        echo "no ports for ${f} in config/config.go; waiting on the process only" >&2
        sleep 1
        kill -0 "$pid" 2>/dev/null
        return
    fi
    min=${range%%:*}
    max=${range##*:}
    pending=()
    for ((p = min; p <= max; p++)); do
        pending+=("$p")
    done
    deadline=$((SECONDS + BENCH_SERVER_START_TIMEOUT))
    while :; do
        if ! kill -0 "$pid" 2>/dev/null; then
            echo "${f} server exited during startup" >&2
            return 1
        fi
        if [ -n "$bench_listen_tool" ]; then
            # One stream, tagged, rather than awk -v: BSD awk refuses a
            # variable with newlines in it.
            still=($({
                bench_listening_ports | sed 's/^/L /'
                printf 'P %s\n' "${pending[@]}"
            } | awk '$1 == "L" { s[$2] = 1; next } !($2 in s) { print $2 }'))
        else
            still=()
            for p in "${pending[@]}"; do
                (exec 3<>"/dev/tcp/127.0.0.1/${p}") 2>/dev/null || still+=("$p")
            done
        fi
        pending=("${still[@]}")
        if [ "${#pending[@]}" -eq 0 ]; then
            return 0
        fi
        if [ "$SECONDS" -ge "$deadline" ]; then
            echo "${f} server not listening on ${#pending[@]} of its ports (${pending[0]}...) after ${BENCH_SERVER_START_TIMEOUT}s" >&2
            return 1
        fi
        sleep 0.1
    done
}

# Waits up to BENCH_SERVER_STOP_TIMEOUT for pid to exit, then kills it.
bench_wait_server_exit() {
    local f=$1 pid=$2 deadline
    [ -n "$pid" ] || return 0
    deadline=$((SECONDS + BENCH_SERVER_STOP_TIMEOUT))
    while kill -0 "$pid" 2>/dev/null; do
        if [ "$SECONDS" -ge "$deadline" ]; then
            echo "${f} server still running ${BENCH_SERVER_STOP_TIMEOUT}s after SIGINT, killing it" >&2
            kill -9 "$pid" 2>/dev/null
            sleep 0.5
            return 0
        fi
        sleep 0.1
    done
}

# Starts f's server with server_flags, the ones the driver forwards to every
# server, and returns once it is listening on all of its ports. A server that
# fails to come up - a port still taken, say - is tried again before this
# gives up on it, BENCH_SERVER_RETRY_DELAY apart: long enough together to
# outlast the minute a port the client before it used stays in TIME_WAIT.
bench_start_server() {
    local f=$1 attempt attempts=3 pid log uws_threads i
    log=$(bench_server_log "$f")
    for ((attempt = 1; attempt <= attempts; attempt++)); do
        preffix="" suffix="" ./script/server.sh "$f" $server_flags $(bench_server_args "$f")
        pid=$(cat "$(bench_server_pidfile "$f")" 2>/dev/null)
        if [ -n "$pid" ] && bench_wait_server_ready "$f" "$pid"; then
            break
        fi
        [ -n "$pid" ] && kill -9 "$pid" 2>/dev/null
        echo "${f} server failed to start (attempt ${attempt}/${attempts}); last of ${log}:" >&2
        tail -n 20 "$log" >&2
        if [ "$attempt" -eq "$attempts" ]; then
            return 1
        fi
        sleep "$BENCH_SERVER_RETRY_DELAY"
    done
    echo "${f} server is up (pid ${pid})"
    # uwebsockets sizes its event loops itself, against the CPUs it was given,
    # so say what it built: the multiplier env.sh prints is what was asked
    # for, 0 for the server's own sizing.
    if [ "$f" = uwebsockets ]; then
        uws_threads=""
        for ((i = 0; i < 50; i++)); do
            uws_threads=$(grep -m1 "^uwebsockets threads:" "$log" 2>/dev/null) && break
            sleep 0.1
        done
        echo "${uws_threads:-uwebsockets threads: not logged yet, see $log}"
    fi
}

# Stops f's server with SIGINT, so it can flush its final statistics, and
# returns once it has exited, so the next server never overlaps it.
bench_stop_server() {
    local f=$1 pidfile pid
    pidfile=$(bench_server_pidfile "$f")
    pid=$(cat "$pidfile" 2>/dev/null)
    . ./script/killone.sh "${f}.server"
    bench_wait_server_exit "$f" "$pid"
    rm -f "$pidfile"
}

# SleepTime seconds of quiet between two measurements, so the one before has
# let go of its sockets and memory. Called before each measurement but the
# first, so nothing waits after the last one: bench_pause_needed says whether
# a measurement came before this one.
bench_pause_needed=false
bench_pause() {
    local i
    if [ "$bench_pause_needed" != true ]; then
        return 0
    fi
    bench_pause_needed=false
    for ((i = 1; i <= SleepTime; i++)); do
        echo "sleep $i ..."
        sleep 1
    done
}
