#!/bin/bash

# Also support invoking this script directly from the repository root.
if [ -z "${BENCH_CLIENT:-}" ] || ! declare -p frameworks >/dev/null 2>&1; then
    . ./script/env.sh || { return 1 2>/dev/null || exit 1; }
fi

build_benchmark() {
    case "$BENCH_CLIENT" in
        benchcli-go|benchcli-uwscpp) ;;
        *) echo "Unsupported BENCH_CLIENT: $BENCH_CLIENT" >&2; return 1 ;;
    esac
    . ./script/clean.sh
    mkdir -p ./output/bin ./output/log ./output/report || return 1

    for f in "${frameworks[@]}"; do
        echo "build ${f} ..."
        case "${f}" in
            uws_events) go build -o "./output/bin/${f}.server" ./frameworks/uws || return 1 ;;
            uws_std) go build -tags=stdio -o "./output/bin/${f}.server" ./frameworks/uws || return 1 ;;
            uwebsockets) bash ./frameworks/uwebsockets/build.sh "$(pwd)/output/bin/${f}.server" || return 1 ;;
            *) go build -o "./output/bin/${f}.server" "./frameworks/${f}" || return 1 ;;
        esac
        echo "build ${f} done"
        echo
    done

    echo "build client: ${BENCH_CLIENT} ..."
    case "$BENCH_CLIENT" in
        benchcli-go) go build -o ./output/bin/bench.client ./benchcli-go || return 1 ;;
        benchcli-uwscpp) bash ./benchcli-uwscpp/build.sh || return 1 ;;
    esac
    echo "build client done"
}

build_benchmark || { return 1 2>/dev/null || exit 1; }
