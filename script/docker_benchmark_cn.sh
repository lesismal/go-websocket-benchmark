#!/usr/bin/env bash
# script/docker_benchmark.sh with the image built from mirrors reachable from
# mainland China: Docker Hub, Debian apt, the Go module proxy, GitHub, the Rust
# toolchain and crates.io. Only the build downloads anything; the benchmark
# runs with --network none either way, so the numbers are the same as
# script/docker_benchmark.sh's.
#
# Every option and flag is script/docker_benchmark.sh's; see its --help. Each
# mirror below can be overridden, or set to an empty value to use the upstream:
#   DOCKER_BENCH_GITHUB_MIRROR= bash script/docker_benchmark_cn.sh --smoke
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# ${VAR-default}, not ${VAR:-default}: an empty value is kept, to go direct.
export DOCKER_BENCH_GO_IMAGE=${DOCKER_BENCH_GO_IMAGE-docker.m.daocloud.io/library/golang:1.27-bookworm}
export DOCKER_BENCH_APT_MIRROR=${DOCKER_BENCH_APT_MIRROR-https://mirrors.aliyun.com}
export DOCKER_BENCH_GOPROXY=${DOCKER_BENCH_GOPROXY-https://goproxy.cn}
# Third-party proxies, tried in order and then https://github.com/ itself;
# what they serve is checked against pinned SHA-256s, so one that fails or
# serves something else only costs a retry. Add or replace one of the same
# https://<proxy>/https://github.com/ form if these stop working.
export DOCKER_BENCH_GITHUB_MIRROR=${DOCKER_BENCH_GITHUB_MIRROR-https://ghfast.top/https://github.com/ https://gh-proxy.com/https://github.com/}
# rsproxy.cn for the Rust toolchain and the crates benchcli-rust and
# frameworks/sockudo_ws build with. rustup-init is checked against a pinned
# SHA-256 and every crate against Cargo.lock, and a failure falls back to the
# upstream (script/docker_rust.sh).
export DOCKER_BENCH_RUSTUP_MIRROR=${DOCKER_BENCH_RUSTUP_MIRROR-https://rsproxy.cn}
export DOCKER_BENCH_CARGO_MIRROR=${DOCKER_BENCH_CARGO_MIRROR-sparse+https://rsproxy.cn/index/}

exec bash "$script_dir/docker_benchmark.sh" "$@"
