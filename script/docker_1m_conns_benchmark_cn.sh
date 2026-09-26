#!/usr/bin/env bash
# script/docker_1m_conns_benchmark.sh with the image built from mirrors
# reachable from mainland China, as script/docker_benchmark_cn.sh builds it.
# Only the build downloads anything; the benchmark runs with --network none
# either way, so the numbers are the same as script/docker_1m_conns_benchmark.sh's.
#
# Every option and flag is script/docker_1m_conns_benchmark.sh's, and every
# mirror script/docker_benchmark_cn.sh's, overridden the same way:
#   DOCKER_BENCH_GITHUB_MIRROR= bash script/docker_1m_conns_benchmark_cn.sh
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

export DOCKER_BENCH_SCRIPT=script/1m_conns_benchmark.sh

exec bash "$script_dir/docker_benchmark_cn.sh" "$@"
