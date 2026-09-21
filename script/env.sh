#!/bin/bash

. ./script/config.sh

# Benchmark client: benchcli-uwscpp (default) or benchcli-go.
# May also be overridden as: BENCH_CLIENT=benchcli-go bash script/benchmark.sh
BENCH_CLIENT=${BENCH_CLIENT:-benchcli-uwscpp}
case "$BENCH_CLIENT" in
    benchcli-go|benchcli-uwscpp) ;;
    *) echo "Unsupported BENCH_CLIENT: $BENCH_CLIENT" >&2; return 1 ;;
esac

if command -v taskset >/dev/null 2>&1; then
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
    echo "benchmark client: ${BENCH_CLIENT}"
    echo $line
    echo "go env:"
    echo
    go env
}
