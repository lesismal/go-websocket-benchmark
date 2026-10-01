#!/bin/bash

# Also support invoking this script directly from the repository root.
if [ -z "${BENCH_CLIENT:-}" ] || ! declare -p frameworks >/dev/null 2>&1; then
    . ./script/env.sh || { return 1 2>/dev/null || exit 1; }
fi

# -buildvcs=false: go build otherwise runs git to stamp the binaries, and fails
# with "error obtaining VCS status: exit status 128" wherever git refuses the
# checkout - e.g. run as root (sudo, for the fd limit) on a checkout another
# user owns. Nothing here reads the stamp.
go_build() { go build -buildvcs=false "$@"; }

build_benchmark() {
    case "$BENCH_CLIENT" in
        benchcli-go|benchcli-rust|benchcli-uwscpp) ;;
        *) echo "Unsupported BENCH_CLIENT: $BENCH_CLIENT" >&2; return 1 ;;
    esac
    . ./script/clean.sh
    mkdir -p ./output/bin ./output/log ./output/report || return 1

    # Each half only builds what it runs, so a client node needs no C++ server
    # toolchain and a server node needs none of the client's.
    if bench_runs_servers; then
        for f in "${frameworks[@]}"; do
            echo "build ${f} ..."
            case "${f}" in
                tokio_tungstenite) bash ./frameworks/tokio_tungstenite/build.sh "$(pwd)/output/bin/${f}.server" || return 1 ;;
                uwebsockets) bash ./frameworks/uwebsockets/build.sh "$(pwd)/output/bin/${f}.server" || return 1 ;;
                uws_events) go_build -o "./output/bin/${f}.server" ./frameworks/uws || return 1 ;;
                uws_std) go_build -tags=stdio -o "./output/bin/${f}.server" ./frameworks/uws || return 1 ;;
                *) go_build -o "./output/bin/${f}.server" "./frameworks/${f}" || return 1 ;;
            esac
            echo "build ${f} done"
            echo
        done
    else
        echo "skip building the servers: they run on ${BENCH_SERVER_HOST}"
        echo
    fi

    if bench_runs_clients; then
        echo "build client: ${BENCH_CLIENT} ..."
        case "$BENCH_CLIENT" in
            benchcli-go) go_build -o ./output/bin/bench.client ./benchcli-go || return 1 ;;
            benchcli-rust) bash ./benchcli-rust/build.sh "$(pwd)/output/bin/bench.client" || return 1 ;;
            benchcli-uwscpp) bash ./benchcli-uwscpp/build.sh || return 1 ;;
        esac
        echo "build client done"
    else
        echo "skip building the client: it runs elsewhere"
    fi
}

build_benchmark || { return 1 2>/dev/null || exit 1; }
