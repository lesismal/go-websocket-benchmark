#!/usr/bin/env bash
# The Rust half of script/Dockerfile.benchmark, for benchcli-rust and frameworks/sockudo_ws:
#
#   docker_rust.sh install           installs the pinned toolchain into RUSTUP_HOME/CARGO_HOME
#   docker_rust.sh fetch <dir>...    downloads every crate each <dir>/Cargo.lock pins into
#                                    CARGO_HOME, so that the builds in the container - which
#                                    runs with --network none - can run offline
#
# Two steps rather than one so that an edit to Cargo.lock does not install the toolchain again.
#
# Where they download from; empty means the upstream default:
#   RUSTUP_DIST_SERVER  stands in for https://static.rust-lang.org, for rustup-init and the
#                       toolchain, e.g. https://rsproxy.cn. rustup-init is checked against a
#                       pinned SHA-256 and the toolchain against its channel manifest
#   CARGO_REGISTRY      a sparse index standing in for crates.io, e.g.
#                       sparse+https://rsproxy.cn/index/. Every crate is checked against the
#                       checksum Cargo.lock records for it
# A mirror that fails is retried against the upstream.
set -euo pipefail

rust_version=1.98.1
rustup_version=1.29.1
upstream=https://static.rust-lang.org

install_toolchain() {
    local arch sha256
    case "$(uname -m)" in
        x86_64|amd64)
            arch=x86_64-unknown-linux-gnu
            sha256=dda7234360b7f578ca8b0ddcb80145646fa61a67c1720a5abc7051b35c9fcb71 ;;
        aarch64|arm64)
            arch=aarch64-unknown-linux-gnu
            sha256=15f6e4ce9f583b929c996c91562bad6d4454f3281de858b02cdfdef615fac433 ;;
        *) echo "no pinned rustup-init for $(uname -m)" >&2; return 1 ;;
    esac

    local download_dir server fetched=false
    download_dir=$(mktemp -d)
    trap 'rm -rf "$download_dir"' RETURN
    for server in ${RUSTUP_DIST_SERVER:-} "$upstream"; do
        local url="$server/rustup/archive/$rustup_version/$arch/rustup-init"
        if curl -fsSL --retry 1 --connect-timeout 10 --max-time 300 -o "$download_dir/rustup-init" "$url" \
            && echo "$sha256  $download_dir/rustup-init" | sha256sum -c --quiet - >/dev/null 2>&1; then
            echo "fetched $url"
            fetched=true
            break
        fi
        echo "failed or checksum mismatch, trying the next source: $url" >&2
    done
    if [ "$fetched" != true ]; then
        echo "no source served rustup-init $rustup_version with SHA-256 $sha256" >&2
        return 1
    fi
    chmod +x "$download_dir/rustup-init"
    "$download_dir/rustup-init" -y --no-modify-path --profile minimal --default-toolchain none

    for server in ${RUSTUP_DIST_SERVER:-} "$upstream"; do
        if RUSTUP_DIST_SERVER=$server rustup toolchain install "$rust_version" --profile minimal; then
            rustup default "$rust_version"
            rustc --version
            cargo --version
            return 0
        fi
        echo "installing Rust $rust_version from $server failed, trying the next source" >&2
    done
    return 1
}

# Every directory's crates come from one source. Cargo files what it downloads under the source
# it came from, and the config naming that source is left in place for the offline builds to
# find it by, so a mirror that serves one lock file but not the next sends all of them to
# crates.io.
fetch_crates() {
    local config="$CARGO_HOME/config.toml" manifest_dir
    # cargo fetch reads the manifest, and a manifest with no target does not parse, so a
    # Cargo.toml/Cargo.lock pair copied on its own gets a stub to fetch for. Which crates it
    # fetches depends on the lock file alone.
    for manifest_dir in "$@"; do
        if [ ! -f "$manifest_dir/src/main.rs" ]; then
            mkdir -p "$manifest_dir/src"
            echo 'fn main() {}' > "$manifest_dir/src/main.rs"
        fi
    done
    if [ -n "${CARGO_REGISTRY:-}" ]; then
        mkdir -p "$CARGO_HOME"
        cat > "$config" <<EOF
[source.crates-io]
replace-with = "mirror"

[source.mirror]
registry = "$CARGO_REGISTRY"
EOF
        local mirrored=true
        for manifest_dir in "$@"; do
            cargo fetch --locked --manifest-path "$manifest_dir/Cargo.toml" || { mirrored=false; break; }
        done
        [ "$mirrored" = true ] && return 0
        echo "fetching the crates from $CARGO_REGISTRY failed, trying crates.io" >&2
        rm -f "$config"
    fi
    for manifest_dir in "$@"; do
        cargo fetch --locked --manifest-path "$manifest_dir/Cargo.toml"
    done
}

case "${1:-}" in
    install) install_toolchain ;;
    fetch)
        shift
        [ "$#" -gt 0 ] || { echo "usage: $0 fetch <directory with Cargo.toml and Cargo.lock>..." >&2; exit 2; }
        fetch_crates "$@" ;;
    *) echo "usage: $0 install | fetch <directory>..." >&2; exit 2 ;;
esac
