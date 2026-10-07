#!/bin/bash

# Guarded, so that config.sh's checks on BENCH_CLIENT, BENCH_ROLE,
# BENCH_TASKPOOL, BENCH_REPORT_SORT and BENCH_FRAMEWORKS actually stop a run.
# They each print and "return 1", but a sourced script's return only sets $?
# in its caller, so without this every entry point sourced env.sh, got 0 back
# from the function definition at the end of it, and ran the whole benchmark on
# a value it had already rejected. Every caller of env.sh already guards it the
# same way; docker_benchmark.sh guards config.sh directly.
. ./script/config.sh || return 1

# Which half of the benchmark this machine runs; see BENCH_ROLE in config.sh.
# The drivers ask through these rather than each testing the variable.
bench_runs_servers() { [ "$BENCH_ROLE" != client ]; }
bench_runs_clients() { [ "$BENCH_ROLE" != server ]; }
# The servers are ours to stop only when we are the machine that started them.
bench_owns_servers() { [ "$BENCH_ROLE" = both ]; }

# Whether fs.file-max leaves room for the connections this run asks for.
# Every connection is an open file at each end, and both ends count when the
# client and the servers share this machine, so a million connections are two
# million files. The limit is system-wide - a container shares the host's, or
# Docker Desktop's VM's - and once it is reached socket() and accept() fail
# with ENFILE everywhere: the client's last dials fail, a server that treats a
# failed accept as fatal (uws_events) exits with every connection it held, and
# the client cannot open the socket of a control request or even its report
# file, so the framework is left out of the report. Checked before the run so
# that it stops here, saying what to raise, rather than one framework at a
# time. $1 is the connection count the clients default to; the rest are the
# client flags, whose last -c wins as it does in the clients.
bench_check_file_max() {
    local conns=$1 arg take=false
    shift
    for arg in "$@"; do
        if [ "$take" = true ]; then
            conns=$arg
            take=false
            continue
        fi
        case "$arg" in
            -c=*|--c=*) conns=${arg#*=} ;;
            -c|--c) take=true ;;
        esac
    done
    # A malformed -c is the clients' to reject.
    [[ "$conns" =~ ^[0-9]+$ ]] || return 0
    # CAP_SYS_ADMIN (bit 21) goes past fs.file-max, as root outside a container does.
    local caps
    caps=$(awk '/^CapEff:/ {print $2}' /proc/self/status 2>/dev/null)
    if [ -n "$caps" ] && (( (16#$caps >> 21) & 1 )); then
        return 0
    fi
    local allocated unused max
    read -r allocated unused max < /proc/sys/fs/file-nr 2>/dev/null || return 0
    [[ "$allocated" =~ ^[0-9]+$ && "$max" =~ ^[0-9]+$ ]] || return 0
    local ends=0
    bench_runs_servers && ends=$((ends + 1))
    bench_runs_clients && ends=$((ends + 1))
    # The servers' listeners, pollers and control connections, the dials in
    # flight and the accepted sockets of the ones that timed out.
    local spare=65536
    local needed=$((conns * ends + spare))
    if [ $((max - allocated)) -ge "$needed" ]; then
        return 0
    fi
    local suggested=$(( (allocated + needed + 999999) / 1000000 * 1000000 ))
    {
        echo "fs.file-max is $max with $allocated files already open, too few for this run:"
        echo "$conns connections x $ends end(s) on this machine, plus $spare to spare, need $needed free."
        echo "It is system-wide (a container shares the host's), and running out of it fails the"
        echo "last dials, stops servers whose accept gives up on ENFILE, and leaves the client"
        echo "unable to ask the server for its numbers or write its report."
        echo "Raise it where the kernel runs, or ask for fewer connections with -c:"
        echo "  Linux host:     sudo sysctl -w fs.file-max=$suggested"
        echo "  Docker Desktop: docker run --rm --privileged alpine sysctl -w fs.file-max=$suggested"
    } >&2
    return 1
}

# Only a single-node run has two halves to divide the CPUs between. On a node
# that runs one of them, pinning it to half a machine nothing else is using
# would leave the other half idle, so the default there is the whole node; an
# explicit list still pins it, for a node that shares its CPUs.
split_cpus=true
if ! bench_runs_servers || ! bench_runs_clients; then
    if [ -z "${BENCH_SERVER_CPU_LIST:-}" ] && [ -z "${BENCH_CLIENT_CPU_LIST:-}" ]; then
        split_cpus=false
    fi
fi

if [ "$split_cpus" = true ] && command -v taskset >/dev/null 2>&1; then
    if [ -n "${BENCH_SERVER_CPU_LIST:-}" ] && [ -n "${BENCH_CLIENT_CPU_LIST:-}" ]; then
        # Docker supplies lists from the daemon's effective cpuset. This avoids
        # selecting host CPUs that are not available inside the container.
        server_cpu_list=$BENCH_SERVER_CPU_LIST
        client_cpu_list=$BENCH_CLIENT_CPU_LIST
    else
        total_cpu_num=$(getconf _NPROCESSORS_ONLN)
        server_cpu_num=$((total_cpu_num / 2 - 1))
        client_cpu_num=$((server_cpu_num + 1))
        server_cpu_list="0-${server_cpu_num}"
        client_cpu_list="${client_cpu_num}-$((total_cpu_num - 1))"

        if command -v lscpu >/dev/null 2>&1; then
            topology=$(lscpu -p=CPU,CORE,SOCKET,NODE | awk -F, '$1 !~ /^#/ {print $1 "," $2 "," $3 "," $4}')
            socket_count=$(printf '%s\n' "$topology" | awk -F, '$3 >= 0 {seen[$3] = 1} END {print length(seen)}')
            node_count=$(printf '%s\n' "$topology" | awk -F, '$4 >= 0 {seen[$4] = 1} END {print length(seen)}')
            server_topology_cpus=""
            client_topology_cpus=""
            server_topology_count=0
            client_topology_count=0

            append_cpu_group() {
                target=$1
                cpus=$2
                count=$(printf '%s\n' "$cpus" | awk -F, '{print NF}')
                if [ "$target" = server ]; then
                    server_topology_cpus="${server_topology_cpus}${server_topology_cpus:+,}${cpus}"
                    server_topology_count=$((server_topology_count + count))
                else
                    client_topology_cpus="${client_topology_cpus}${client_topology_cpus:+,}${cpus}"
                    client_topology_count=$((client_topology_count + count))
                fi
            }

            if [ "$socket_count" -ge 2 ]; then
                mapfile -t groups < <(printf '%s\n' "$topology" | awk -F, '$3 >= 0 {print $3}' | sort -n -u)
                group_column=3
            elif [ "$node_count" -ge 2 ]; then
                mapfile -t groups < <(printf '%s\n' "$topology" | awk -F, '$4 >= 0 {print $4}' | sort -n -u)
                group_column=4
            else
                mapfile -t groups < <(printf '%s\n' "$topology" | awk -F, '{print $3 ":" $2}' | sort -t: -k1,1n -k2,2n -u)
                group_column=core
            fi

            for group in "${groups[@]}"; do
                if [ "$group_column" = core ]; then
                    socket=${group%%:*}
                    core=${group#*:}
                    group_cpus=$(printf '%s\n' "$topology" | awk -F, -v socket="$socket" -v core="$core" '$3 == socket && $2 == core {print $1}' | paste -sd, -)
                else
                    group_cpus=$(printf '%s\n' "$topology" | awk -F, -v column="$group_column" -v group="$group" '$column == group {print $1}' | paste -sd, -)
                fi
                if [ "$server_topology_count" -le "$client_topology_count" ]; then
                    append_cpu_group server "$group_cpus"
                else
                    append_cpu_group client "$group_cpus"
                fi
            done

            if [ -n "$server_topology_cpus" ] && [ -n "$client_topology_cpus" ]; then
                server_cpu_list=$server_topology_cpus
                client_cpu_list=$client_topology_cpus
            fi
        fi
    fi

    limit_cpu_server="taskset -c ${server_cpu_list}"
    limit_cpu_client="taskset -c ${client_cpu_list}"
fi

# debug
# echo "limit_cpu_server: ${server_cpu_list}, ${limit_cpu_server}"
# echo "limit_cpu_client: ${client_cpu_list}, ${limit_cpu_client}"

line=$(printf "%0.s-" {1..62})

clean() {
    rm -rf ./output
    for f in ${frameworks[@]}; do
        killall -9 "${f}.server" 1>/dev/null 2>&1
    done
}

# The server settings this config gives a run, for the Summary table the
# report step writes: Pool Size, Event Loops, Loops Per CPU and Socket
# Syscalls, each with the frameworks of this run it reaches, which the row's
# Description names. The clients never see these settings, so no report
# carries them: the driver writes them here, beside the reports, once the run's
# frameworks are known, and script/report.sh run again later reads the run's
# own settings rather than whatever the shell holds by then. A setting no
# framework of this run takes is left out. The words of each row are
# report.SummaryParameters'; see report.ServerParameter.
#
# A client node of a two-node run writes its own settings, so give both nodes
# the same ones.
bench_write_server_parameters() {
    local pooled=() f
    # tokio_tungstenite and uwebsockets serve /taskpool but take no Go pool,
    # nor its sizing; see bench_server_args in script/serverctl.sh.
    for f in "${taskpool_frameworks[@]}"; do
        case "$f" in
            tokio_tungstenite|uwebsockets) ;;
            *) pooled+=("$f") ;;
        esac
    done
    mkdir -p ./output/report
    bench_server_parameter_sep=""
    {
        printf '['
        bench_server_parameter "Pool Size" \
            "min ${BENCH_TASKPOOL_MIN}, max ${BENCH_TASKPOOL_MAX}, queue ${BENCH_TASKPOOL_QUEUE}" "${pooled[@]}"
        bench_server_parameter "Event Loops" "${BENCH_EVENTLOOPS}" "${eventloop_frameworks[@]}"
        bench_server_parameter "Loops Per CPU" "${BENCH_UWS_LOOPS_PER_CPU}" uwebsockets
        bench_server_parameter "Socket Syscalls" "${BENCH_FIB_SOCKET_SYSCALLS}" fib
        printf ']\n'
    } >./output/report/ServerParameters.json
}

# One entry of bench_write_server_parameters' list: its name, its value and
# the frameworks that take it, of which only this run's are written. None of
# them leaves the entry out.
bench_server_parameter() {
    local name=$1 value=$2 f r list=""
    shift 2
    for f in "$@"; do
        for r in "${frameworks[@]}"; do
            if [ "$f" = "$r" ]; then
                list="${list:+${list},}\"${f}\""
            fi
        done
    done
    [ -n "$list" ] || return 0
    # The value is the shell's, so quote what JSON would not take as it is.
    value=${value//\\/\\\\}
    value=${value//\"/\\\"}
    printf '%s{"Name":"%s","Value":"%s","Frameworks":[%s]}' \
        "$bench_server_parameter_sep" "$name" "$value" "$list"
    bench_server_parameter_sep=","
}

print_env() {
    echo "os:"
    echo
    cat /etc/issue
    echo $line
    echo "cpu model:"
    echo
    cat /proc/cpuinfo | grep "model name" | uniq
    echo $line
    echo "processors:"
    echo
    cat /proc/cpuinfo | grep processor
    echo $line
    free
    echo $line
    echo "server cpus: ${server_cpu_list:-unbound}"
    echo "client cpus: ${client_cpu_list:-unbound}"
    echo $line
    echo "role: ${BENCH_ROLE} (servers: $(bench_runs_servers && echo here || echo elsewhere), clients: $(bench_runs_clients && echo here || echo elsewhere))"
    echo "server host: ${BENCH_SERVER_HOST}"
    echo $line
    echo "benchmark client: ${BENCH_CLIENT}"
    echo $line
    echo "taskpool: ${BENCH_TASKPOOL} (min ${BENCH_TASKPOOL_MIN}, max ${BENCH_TASKPOOL_MAX}, queue ${BENCH_TASKPOOL_QUEUE})"
    echo "uwebsockets: echoes from its event loops (BENCH_TASKPOOL does not apply to it)"
    echo "uwebsockets threads asked for: loops ${BENCH_UWS_LOOPS_PER_CPU}/cpu (0 = the server's own sizing; the server's startup line says what it built)"
    echo $line
    echo "report sort: ${BENCH_REPORT_SORT} (result = best first, framework = config.FrameworkList order)"
    echo "fib socket syscalls: ${BENCH_FIB_SOCKET_SYSCALLS} (true = recvfrom/sendto/sendmsg, false = read/write/writev)"
    echo "event loops: ${BENCH_EVENTLOOPS} (0 = each framework's own default; for ${eventloop_frameworks[*]})"
    echo "project: ${BENCH_PROJECT:-(none)}"
    echo $line
    echo "go env:"
    echo
    go env
}
