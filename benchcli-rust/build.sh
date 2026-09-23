#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
if ! command -v cargo >/dev/null 2>&1; then
    echo "benchcli-rust: cargo not found; install a Rust toolchain (https://rustup.rs), 1.85 or newer" >&2
    exit 1
fi
# --locked: build exactly the versions Cargo.lock pins. The first build downloads them from
# crates.io; CARGO_NET_OFFLINE=true builds from what is already in CARGO_HOME, which is how
# script/Dockerfile.benchmark builds it. build.rs runs python3 on
# benchcli-uwscpp/generate_metadata.py, as benchcli-uwscpp's own build does.
cargo build --release --locked ${CARGO_BUILD_ARGS:-}
target_dir=${CARGO_TARGET_DIR:-"$PWD/target"}
output=${1:-../output/bin/bench.client}
mkdir -p "$(dirname "$output")"
cp "$target_dir/release/benchcli-rust" "$output"
