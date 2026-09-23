#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
if ! command -v cargo >/dev/null 2>&1; then
    echo "sockudo_ws: cargo not found; install a Rust toolchain (https://rustup.rs), 1.85 or newer" >&2
    exit 1
fi
# --locked: build exactly the versions Cargo.lock pins, and fail rather than update them. The
# first build downloads them from crates.io; set CARGO_NET_OFFLINE=true to build from what is
# already in CARGO_HOME, which is how script/Dockerfile.benchmark builds it.
cargo build --release --locked ${CARGO_BUILD_ARGS:-}
target_dir=${CARGO_TARGET_DIR:-"$PWD/target"}
output=${1:-../../output/bin/sockudo_ws.server}
mkdir -p "$(dirname "$output")"
cp "$target_dir/release/sockudo_ws_server" "$output"
