#!/bin/bash

. ./script/env.sh

echo "kill all ..."

killcmd=pkill
if [ $(which killall) ]; then
    killcmd=killall
fi

# run
for f in ${frameworks[@]}; do
    echo "kill ${f}.server ..."
    $killcmd -9 "${f}.server" 1>/dev/null 2>&1
done
echo "kill bench.client ..."
$killcmd -9 "bench.client" 1>/dev/null 2>&1

echo "kill all done"
