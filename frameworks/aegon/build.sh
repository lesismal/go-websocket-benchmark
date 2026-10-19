#!/usr/bin/env bash
# Builds the aegon server. Linux only: Aegon runs on io_uring.
#
# What it needs installed: a C++26 compiler whose standard library erases from an
# unordered_map by a heterogeneous key, which is GCC 15's libstdc++ or newer (Aegon's own
# packaging builds with GCC 16); cmake, curl and xz; and the development files of OpenSSL 3.5 or
# newer, liburing and libdeflate. libjemalloc is linked in when it is there, as HttpArena runs
# Aegon (AEGON_JEMALLOC=false leaves it out).
#
# What it fetches, at pinned versions checked against their SHA-256: Aegon itself, glaze, and
# nghttp2, nghttp3 and ngtcp2, which it builds as static libraries. Aegon links its HTTP/2 and
# HTTP/3 stacks into the same library as its WebSocket server, so they have to be there even
# though only the WebSocket route is served; and distributions mostly lack ngtcp2's OpenSSL
# backend and an nghttp2 new enough for Aegon (1.65 or newer).
#
#   AEGON_PREBUILT  a server built already, copied to the output instead of building one. This is
#                   how script/Dockerfile.benchmark gives the benchmark its aegon server: it is
#                   built in an image stage of its own, since the benchmark image's compiler and
#                   libraries are too old for it.
#   AEGON_BUNDLE    a directory to make the server self-contained in: its shared libraries,
#                   glibc and the dynamic loader included, are copied to AEGON_BUNDLE/lib, and the
#                   server is pointed at them. What that image stage builds with.
#   GITHUB_MIRROR   space-separated prefixes standing in for https://github.com/, tried in order
#                   before it, as in script/docker_fetch_deps.sh.
#   CXX, CXXFLAGS, LDFLAGS as usual; CXX defaults to the newest of g++-16, g++-15 and g++.
set -euo pipefail
cd "$(dirname "$0")"

output=${1:-../../output/bin/aegon.server}
mkdir -p "$(dirname "$output")"

if [ -n "${AEGON_PREBUILT:-}" ]; then
    cp "$AEGON_PREBUILT" "$output"
    echo "aegon: copied the prebuilt server $AEGON_PREBUILT"
    exit 0
fi

if [ "$(uname -s)" != Linux ]; then
    echo "aegon: Linux only (Aegon runs on io_uring); script/docker_benchmark.sh runs it elsewhere" >&2
    exit 1
fi

aegon_commit=e6490223aa2529165c75c29c60174c10b02f88a9
deps_dir=${AEGON_DEPS_DIR:-"$PWD/.deps"}
build_dir=${AEGON_BUILD_DIR:-"$PWD/.build"}
prefix=$build_dir/prefix
jobs=$(nproc 2>/dev/null || echo 4)
mkdir -p "$deps_dir" "$build_dir"

# fetch <path under https://github.com/> <sha256> <file>
fetch() {
    local prefix_url
    if [ -f "$3" ] && echo "$2  $3" | sha256sum -c --quiet - >/dev/null 2>&1; then
        return 0
    fi
    for prefix_url in ${GITHUB_MIRROR:-} https://github.com/; do
        if curl -fsSL --retry 1 --connect-timeout 10 --max-time 300 -o "$3" "$prefix_url$1" \
            && echo "$2  $3" | sha256sum -c --quiet - >/dev/null 2>&1; then
            echo "aegon: fetched $prefix_url$1"
            return 0
        fi
        echo "aegon: failed or checksum mismatch, trying the next source: $prefix_url$1" >&2
    done
    echo "aegon: no source served $1 with SHA-256 $2" >&2
    rm -f "$3"
    return 1
}

# source_dir <name> <path under https://github.com/> <sha256>: fetches and unpacks it into
# deps_dir/name, once per checksum.
source_dir() {
    local dir="$deps_dir/$1" archive="$deps_dir/$1.tar.${2##*.tar.}"
    if [ "$(cat "$dir/.sha256" 2>/dev/null)" != "$3" ]; then
        fetch "$2" "$3" "$archive"
        rm -rf "$dir"
        mkdir -p "$dir"
        tar -xf "$archive" -C "$dir" --strip-components=1
        echo "$3" >"$dir/.sha256"
    fi
}

source_dir aegon "UdayKhare09/Aegon/archive/${aegon_commit}.tar.gz" \
    2e0207900dba3ba6f4a02906d203f4fe4eae1d46713878a1ca51f08fdbf34543
source_dir glaze stephenberry/glaze/archive/refs/tags/v9.0.0.tar.gz \
    dd7b033c5bf6e4308615bb7834c541291d5b20f64121d9a68222a39414635517
source_dir nghttp2 nghttp2/nghttp2/releases/download/v1.70.0/nghttp2-1.70.0.tar.xz \
    e05cb1388eaca3830aded4ccf20044b6e1ac1a61411dcca11b0437c4285c8bc2
source_dir nghttp3 ngtcp2/nghttp3/releases/download/v1.18.0/nghttp3-1.18.0.tar.xz \
    aad782c23d3f01bd4bb52c8bac7a553b631ef8115fd1612703df6183449fef19
source_dir ngtcp2 ngtcp2/ngtcp2/releases/download/v1.25.0/ngtcp2-1.25.0.tar.xz \
    2a34d2484ba17847a5d11965704e9dd0fac4c6d8efc75ffe1ec7de66d8c6b6fb

# The compiler: CXX, or the newest GCC that has what Aegon uses of C++26.
probe=$build_dir/probe.cpp
cat >"$probe" <<'EOF'
#include <string>
#include <string_view>
#include <unordered_map>
struct H { using is_transparent = void; size_t operator()(std::string_view s) const { return std::hash<std::string_view>{}(s); } };
struct E { using is_transparent = void; bool operator()(std::string_view a, std::string_view b) const { return a == b; } };
int main() { std::unordered_map<std::string, int, H, E> m; std::string_view k = "k"; return int(m.erase(k)); }
EOF
cxx=""
for candidate in ${CXX:-g++-16 g++-15 g++}; do
    if command -v "$candidate" >/dev/null 2>&1 && "$candidate" -std=c++26 -fsyntax-only "$probe" 2>/dev/null; then
        cxx=$candidate
        break
    fi
done
if [ -z "$cxx" ]; then
    echo "aegon: no C++ compiler here builds Aegon: it needs C++26 and GCC 15's libstdc++ or newer (tried: ${CXX:-g++-16 g++-15 g++})" >&2
    exit 1
fi
echo "aegon: compiling with $cxx $("$cxx" -dumpfullversion 2>/dev/null || true)"

# nghttp2, nghttp3 and ngtcp2 as static libraries in prefix, once per version.
cmake_lib() {
    local name=$1 stamp="$prefix/.$1"
    shift
    if [ "$(cat "$stamp" 2>/dev/null)" = "$(cat "$deps_dir/$name/.sha256")" ]; then
        return 0
    fi
    rm -rf "$build_dir/$name"
    cmake -S "$deps_dir/$name" -B "$build_dir/$name" -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_INSTALL_PREFIX="$prefix" -DCMAKE_INSTALL_LIBDIR=lib \
        -DCMAKE_POSITION_INDEPENDENT_CODE=ON -DBUILD_TESTING=OFF "$@" >/dev/null
    cmake --build "$build_dir/$name" -j"$jobs" >/dev/null
    cmake --install "$build_dir/$name" >/dev/null
    cp "$deps_dir/$name/.sha256" "$stamp"
}
mkdir -p "$prefix"
cmake_lib nghttp2 -DENABLE_LIB_ONLY=ON -DBUILD_SHARED_LIBS=OFF -DBUILD_STATIC_LIBS=ON \
    -DENABLE_DOC=OFF -DENABLE_FAILMALLOC=OFF
cmake_lib nghttp3 -DENABLE_LIB_ONLY=ON -DENABLE_SHARED_LIB=OFF -DENABLE_STATIC_LIB=ON
cmake_lib ngtcp2 -DENABLE_LIB_ONLY=ON -DENABLE_SHARED_LIB=OFF -DENABLE_STATIC_LIB=ON \
    -DENABLE_OPENSSL=ON -DENABLE_GNUTLS=OFF -DENABLE_BORINGSSL=OFF -DENABLE_PICOTLS=OFF \
    -DENABLE_WOLFSSL=OFF
if [ ! -f "$prefix/lib/libngtcp2_crypto_ossl.a" ]; then
    echo "aegon: ngtcp2 built without its OpenSSL backend, which needs OpenSSL 3.5 or newer" >&2
    exit 1
fi

# Aegon's headers are installed as <aegon/...> and include each other from src/; HttpArena's
# server includes them the first way, so give it that path.
include_dir=$build_dir/include
mkdir -p "$include_dir"
ln -sfn "$deps_dir/aegon/src" "$include_dir/aegon"

# The parts of Aegon the server links: aegon_log, aegon_core and aegon_http, by the source lists
# of Aegon's CMakeLists.txt at the pinned commit, with the flags its Release build uses.
aegon_sources=(
    src/log/ConsoleSink.cpp src/log/FileSink.cpp src/log/Logger.cpp
    src/core/BufferPool.cpp src/core/IoUring.cpp src/core/EventLoop.cpp
    src/http/RadixTree.cpp src/http/Router.cpp src/http/Server.cpp
    src/http/v1/Http1Connection.cpp src/http/v1/Http1Serializer.cpp
    src/http/v2/Http2Connection.cpp
    src/http/tls/TlsContext.cpp src/http/tls/TlsStream.cpp
    src/http/v3/Http3Connection.cpp src/http/v3/Http3Server.cpp
    src/http/client/TlsClientStream.cpp src/http/client/ConnectionPool.cpp
    src/http/client/HttpClient.cpp src/http/client/Http2ClientSession.cpp
    src/http/client/Http3ClientSession.cpp
    src/http/websocket/WebSocketSession.cpp src/http/websocket/WebSocketConnection.cpp
)
cxxflags=(-std=c++26 -O3 -march=native -DNDEBUG -pthread ${CXXFLAGS:-})
# -isystem: their warnings are not this build's to fix either, in server.cpp's includes of them.
includes=(-isystem "$include_dir" -isystem "$deps_dir/aegon/src" -isystem "$deps_dir/glaze/include" -isystem "$prefix/include")
obj_dir=$build_dir/obj
obj_stamp="$aegon_commit $cxx ${cxxflags[*]}"
if [ "$(cat "$obj_dir/.stamp" 2>/dev/null)" != "$obj_stamp" ]; then
    rm -rf "$obj_dir"
fi
mkdir -p "$obj_dir"
objects=()
pending=()
for source in "${aegon_sources[@]}"; do
    object=$obj_dir/$(echo "${source%.cpp}" | tr / _).o
    objects+=("$object")
    [ -f "$object" ] || pending+=("$source")
done
if [ "${#pending[@]}" -gt 0 ]; then
    echo "aegon: compiling ${#pending[@]} Aegon sources, $jobs at a time"
    for source in "${pending[@]}"; do
        while [ "$(jobs -rp | wc -l)" -ge "$jobs" ]; do
            wait -n || true
        done
        object=$obj_dir/$(echo "${source%.cpp}" | tr / _).o
        # -w: Aegon's own warnings are not this build's to fix.
        ("$cxx" "${cxxflags[@]}" "${includes[@]}" -w -c "$deps_dir/aegon/$source" -o "$object.tmp" \
            && mv "$object.tmp" "$object") &
    done
    wait || true
    for object in "${objects[@]}"; do
        [ -f "$object" ] || { echo "aegon: failed to compile $object" >&2; exit 1; }
    done
fi
echo "$obj_stamp" >"$obj_dir/.stamp"

malloc_lib=()
case "${AEGON_JEMALLOC:-auto}" in
    false) ;;
    true) malloc_lib=(-ljemalloc) ;;
    *)
        if echo 'int main() { return 0; }' | "$cxx" -x c++ - -ljemalloc -o "$build_dir/probe" 2>/dev/null; then
            malloc_lib=(-ljemalloc)
        fi ;;
esac
echo "aegon: allocator: ${malloc_lib[*]:-glibc malloc}"

# With AEGON_BUNDLE the server is linked to load every shared library from AEGON_BUNDLE/lib: the
# loader by an absolute path, which is what an interpreter has to be, and the libraries by
# DT_RPATH rather than DT_RUNPATH (--disable-new-dtags), which only the server's own dependencies
# would be looked up in and not theirs (libssl's libcrypto, say). Set when linking rather than
# rewritten afterwards: patchelf's rewrite of the non-PIE executable the GCC image links by
# default crashed it before main on x86_64.
bundle_ldflags=()
if [ -n "${AEGON_BUNDLE:-}" ]; then
    lib_dir=$AEGON_BUNDLE/lib
    echo 'int main() { return 0; }' | "$cxx" -x c++ - -o "$build_dir/probe-interpreter"
    interpreter=$(readelf -lW "$build_dir/probe-interpreter" | sed -n 's/.*program interpreter: \(.*\)]$/\1/p')
    if [ -z "$interpreter" ]; then
        echo "aegon: cannot tell which dynamic loader $cxx links against" >&2
        exit 1
    fi
    bundle_ldflags=(-Wl,--dynamic-linker="$lib_dir/$(basename "$interpreter")"
        -Wl,--disable-new-dtags -Wl,-rpath,"$lib_dir")
fi

# jemalloc ahead of libc, so that its malloc is the one every library ends up calling.
"$cxx" "${cxxflags[@]}" -Wall -Wextra "${includes[@]}" server.cpp "${objects[@]}" \
    -L"$prefix/lib" -lngtcp2_crypto_ossl -lngtcp2 -lnghttp3 -lnghttp2 \
    ${malloc_lib[@]+"${malloc_lib[@]}"} -luring -ldeflate -lssl -lcrypto \
    ${bundle_ldflags[@]+"${bundle_ldflags[@]}"} ${LDFLAGS:-} -o "$output"

if [ -n "${AEGON_BUNDLE:-}" ]; then
    # Listed by the system's own loader, which finds them where they are installed while lib_dir
    # is empty.
    rm -rf "$lib_dir"
    mkdir -p "$lib_dir"
    "$interpreter" --list "$output" | awk '$2 == "=>" && $3 ~ /^\// { print $3 }' | while read -r lib; do
        cp -L "$lib" "$lib_dir/"
    done
    cp -L "$interpreter" "$lib_dir/"
    echo "aegon: bundled $(ls "$lib_dir" | wc -l) libraries into $lib_dir"
fi
