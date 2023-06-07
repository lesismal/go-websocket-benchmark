#!/bin/bash

. ./script/env.sh

echo "kill all ..."

killcmd=pkill
if [ $(which killall) ]; then
    killcmd=killall
fi

# run
for f in ${frameworks[@]}; do
    $killcmd "${f}.server"
done
$killcmd "bench.client"

echo "kill all done"
