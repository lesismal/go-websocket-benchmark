#!/bin/bash

. ./script/env.sh

echo "kill all ..."

# run
for f in ${frameworks[@]}; do
    echo "kill ${f}.server ..."
    killall -9 "${f}.server" 1>/dev/null 2>&1
done
killall -9 "bench.client" 1>/dev/null 2>&1

echo "kill all done"
