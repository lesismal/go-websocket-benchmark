#!/bin/bash

. ./script/config.sh

total_cpu_num=$(getconf _NPROCESSORS_ONLN)
server_cpu_num=$((total_cpu_num >= 16 ? 7 : total_cpu_num / 2 - 1))
client_cpu_num=$((server_cpu_num + 1))
limit_cpu_server="taskset -c 0-${server_cpu_num}"
limit_cpu_client="taskset -c ${client_cpu_num}-$((total_cpu_num - 1))"

# debug
# echo "limit_cpu_server: ${server_cpu_num}, ${limit_cpu_server}"
# echo "limit_cpu_client: ${client_cpu_num}, ${limit_cpu_client}"

line="--------------------------------"

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
    echo "go env:"
    echo
    go env
}
