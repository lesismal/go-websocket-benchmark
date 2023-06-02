#!/bin/bash

. ./script/env.sh

echo "kill all ..."

killcmd=pkill
if [ $(which killall) ]; then
    killcmd=killall
fi

# run
for f in ${frameworks[@]}; do
    . ./script/killone.sh "${f}.server"
done
. ./script/killone.sh "bench.client"

echo "kill all done"
