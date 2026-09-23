#!/usr/bin/env bash
# Puts the pinned native dependencies where benchcli-uwscpp/build.sh and
# frameworks/uwebsockets/build.sh look for them. Run by script/Dockerfile.benchmark.
#
# Release tarballs and a single header rather than git clones: about 1.3 MB in
# three requests, where the depth-1 clones were about 11 MB (nlohmann/json's
# pack alone is 9.6 MB, for the one header the client includes) over a dozen
# round trips, and uWebSockets was fetched twice. Every file is checked against
# its SHA-256, so a mirror that serves anything else is skipped rather than
# built from.
#
# GITHUB_MIRROR is a space-separated list of prefixes that stand in for
# https://github.com/, tried in order before https://github.com/ itself.
set -euo pipefail

workspace=${1:-/workspace}
download_dir=$(mktemp -d)
trap 'rm -rf "$download_dir"' EXIT

# fetch <path under https://github.com/> <sha256> <output file>
fetch() {
    local prefix
    for prefix in ${GITHUB_MIRROR:-} https://github.com/; do
        if curl -fsSL --retry 1 --connect-timeout 10 --max-time 300 -o "$3" "$prefix$1" \
            && echo "$2  $3" | sha256sum -c --quiet - >/dev/null 2>&1; then
            echo "fetched $prefix$1"
            return 0
        fi
        echo "failed or checksum mismatch, trying the next source: $prefix$1" >&2
    done
    echo "no source served $1 with SHA-256 $2" >&2
    return 1
}

# uSockets is the one uWebSockets submodule the builds use, at the commit
# v20.74.0 records for it.
fetch uNetworking/uWebSockets/archive/refs/tags/v20.74.0.tar.gz \
    e1d9c99b8e87e78a9aaa89ca3ebaa450ef0ba22304d24978bb108777db73676c \
    "$download_dir/uWebSockets.tar.gz" &
uwebsockets_pid=$!
fetch uNetworking/uSockets/archive/182b7e4fe7211f98682772be3df89c71dc4884fa.tar.gz \
    a11dced81a66af897c77e6bb37101a04e675a74c377cc1f00b7ae4a6ad5338b7 \
    "$download_dir/uSockets.tar.gz" &
usockets_pid=$!
fetch nlohmann/json/releases/download/v3.12.0/json.hpp \
    aaf127c04cb31c406e5b04a63f1ae89369fccde6d8fa7cdda1ed4f32dfc5de63 \
    "$download_dir/json.hpp" &
json_pid=$!
status=0
wait "$uwebsockets_pid" || status=1
wait "$usockets_pid" || status=1
wait "$json_pid" || status=1
[ "$status" -eq 0 ] || exit 1

uwebsockets=$workspace/benchcli-uwscpp/.deps/uWebSockets
mkdir -p "$uwebsockets/uSockets"
tar -xzf "$download_dir/uWebSockets.tar.gz" -C "$uwebsockets" --strip-components=1
tar -xzf "$download_dir/uSockets.tar.gz" -C "$uwebsockets/uSockets" --strip-components=1

mkdir -p "$workspace/frameworks/uwebsockets/.deps"
cp -a "$uwebsockets" "$workspace/frameworks/uwebsockets/.deps/uWebSockets"

json_include=$workspace/benchcli-uwscpp/.deps/json/single_include/nlohmann
mkdir -p "$json_include"
cp "$download_dir/json.hpp" "$json_include/json.hpp"
