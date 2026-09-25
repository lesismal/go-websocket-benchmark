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
    echo "uwebsockets logic pool: on for uwebsockets, off for uwebsockets-inline (BENCH_TASKPOOL does not apply to it)"
    echo "uwebsockets threads asked for: workers ${BENCH_UWS_WORKERS_PER_CPU}/cpu, loops ${BENCH_UWS_LOOPS_PER_CPU}/cpu (0 = the server's own sizing; the server's startup line says what it built)"
    echo $line
    echo "report sort: ${BENCH_REPORT_SORT} (result = best first, framework = config.FrameworkList order)"
    echo "project: ${BENCH_PROJECT:-(none)}"
    echo $line
    echo "go env:"
    echo
    go env
}
