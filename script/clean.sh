#!/bin/bash

# . ./script/env.sh

echo "clean ..."

if declare -F bench_owns_servers >/dev/null && ! bench_owns_servers; then
    # Servers we did not start: on a client node there are none to clean, and
    # on a machine whose servers are already up their binaries are still
    # running and their logs are still being written. Only what this half owns.
    rm -rf ./output/report
    rm -f ./output/bin/bench.client
else
    rm -rf ./output
fi

echo "clean done"
