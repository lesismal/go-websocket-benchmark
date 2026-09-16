#!/bin/bash

# . ./script/env.sh

. ./script/clean.sh

mkdir -p ./output/bin
mkdir -p ./output/log
mkdir -p ./output/report

# build
for f in ${frameworks[@]}; do
    echo "build ${f} ..."
    case "${f}" in
    "uws_events")
        go build -o "./output/bin/${f}.server" "./frameworks/uws"
        ;;
    "uws_std")
        go build -tags=stdio -o "./output/bin/${f}.server" "./frameworks/uws"
        ;;
    *)
        go build -o "./output/bin/${f}.server" "./frameworks/${f}"
        ;;
    esac
    echo "build ${f} done"
    echo
done
echo "build client ..."
go build -o "./output/bin/bench.client" "./mwsbench"
echo "build client done"
