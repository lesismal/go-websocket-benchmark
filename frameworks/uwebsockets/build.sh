#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
# Only uSockets is needed; recursive checkout would also fetch fuzzers and TLS libraries.
uwebsockets=${UWEBSOCKETS_DIR:-"$PWD/.deps/uWebSockets"}
if [ ! -f "$uwebsockets/src/App.h" ]; then
    mkdir -p "$(dirname "$uwebsockets")"
    git clone --depth 1 --branch v20.74.0 https://github.com/uNetworking/uWebSockets.git "$uwebsockets"
fi
if [ ! -f "$uwebsockets/uSockets/src/libusockets.h" ]; then
    git -C "$uwebsockets" submodule update --init --depth 1 uSockets
fi
# Deliberately not named BUILD_DIR: script/Dockerfile.benchmark points BUILD_DIR at
# benchcli-uwscpp's own object directory, and both projects compile identically named
# uSockets translation units, so sharing that variable would let one build clobber the other.
build_dir=${UWEBSOCKETS_SERVER_BUILD_DIR:-"$PWD/.build"}
mkdir -p "$build_dir"
objects=()
for source in "$uwebsockets"/uSockets/src/*.c "$uwebsockets"/uSockets/src/eventing/*.c "$uwebsockets"/uSockets/src/crypto/*.c "$uwebsockets"/uSockets/src/io_uring/*.c; do
    object="$build_dir/$(basename "${source%.c}").o"
    "${CC:-cc}" -std=c11 -O3 -DLIBUS_NO_SSL -I"$uwebsockets/uSockets/src" ${CFLAGS:-} -c "$source" -o "$object"
    objects+=("$object")
done
output=${1:-../../output/bin/uwebsockets.server}
mkdir -p "$(dirname "$output")"
"${CXX:-c++}" -std=c++20 -O3 -Wall -Wextra -pthread -DLIBUS_NO_SSL \
    -I"$uwebsockets/src" -I"$uwebsockets/uSockets/src" \
    ${CXXFLAGS:-} server.cpp "${objects[@]}" ${LDFLAGS:-} -lz -o "$output"
