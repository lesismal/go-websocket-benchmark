#!/usr/bin/env bash
# script/1m_conns_benchmark.sh - a million connections, 1KiB payload - in
# script/docker_benchmark.sh's resource-limited container, which builds the
# image, divides the CPUs between the servers and the client and copies the
# results to output/docker/<timestamp> as it does for script/benchmark.sh.
#
# Every option and flag is script/docker_benchmark.sh's; see its --help, but
# --smoke, which is script/benchmark.sh's. BENCH_FRAMEWORKS picks from
# script/1m_conns_benchmark.sh's list, and flags after the options go to its
# clients after its own -c=1000000 -en=2000000 -b=1024 -rr=1, so they win:
#   BENCH_FRAMEWORKS=fib,fib-inline bash script/docker_1m_conns_benchmark.sh
#   bash script/docker_1m_conns_benchmark.sh -c=200000
#
# Both sides of each connection are in the one container, so its memory limit
# has to hold two million sockets besides the servers and the client: give it
# plenty with DOCKER_BENCH_MEMORY (and Docker Desktop's VM the memory to back
# it), or ask for fewer connections with -c.
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

export DOCKER_BENCH_SCRIPT=script/1m_conns_benchmark.sh

exec bash "$script_dir/docker_benchmark.sh" "$@"
