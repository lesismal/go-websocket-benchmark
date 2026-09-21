#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
# Only uSockets is needed; recursive checkout would also fetch fuzzers and TLS libraries.
uwebsockets=${UWEBSOCKETS_DIR:-"$PWD/.deps/uWebSockets"}
json_dir=${NLOHMANN_JSON_DIR:-"$PWD/.deps/json"}
if [ ! -f "$uwebsockets/src/WebSocketProtocol.h" ]; then
    mkdir -p "$(dirname "$uwebsockets")"
    git clone --depth 1 --branch v20.74.0 https://github.com/uNetworking/uWebSockets.git "$uwebsockets"
fi
if [ ! -f "$uwebsockets/uSockets/src/libusockets.h" ]; then
    git -C "$uwebsockets" submodule update --init --depth 1 uSockets
fi
if [ ! -f "$json_dir/single_include/nlohmann/json.hpp" ]; then
    mkdir -p "$(dirname "$json_dir")"
    git clone --depth 1 --branch v3.12.0 https://github.com/nlohmann/json.git "$json_dir"
fi
build_dir=${BUILD_DIR:-"$PWD/.build"}
mkdir -p "$build_dir"
python3 generate_metadata.py "$build_dir/metadata.hpp"
# Keep object files outside the dependency checkout, including with an offline source tree.
objects=()
for source in "$uwebsockets"/uSockets/src/*.c "$uwebsockets"/uSockets/src/eventing/*.c "$uwebsockets"/uSockets/src/crypto/*.c "$uwebsockets"/uSockets/src/io_uring/*.c; do
    object="$build_dir/$(basename "${source%.c}").o"
    "${CC:-cc}" -std=c11 -O3 -DLIBUS_NO_SSL -I"$uwebsockets/uSockets/src" ${CFLAGS:-} -c "$source" -o "$object"
    objects+=("$object")
done
output=${1:-../output/bin/bench.client}
mkdir -p "$(dirname "$output")"
"${CXX:-c++}" -std=c++17 -O3 -Wall -Wextra -pthread -DLIBUS_NO_SSL \
    -I"$uwebsockets/src" -I"$uwebsockets/uSockets/src" \
    -I"$json_dir/single_include" -I"$build_dir" \
    ${CXXFLAGS:-} main.cpp "${objects[@]}" ${LDFLAGS:-} -lcurl -o "$output"
