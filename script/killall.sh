#!/bin/bash

. ./script/env.sh

echo "kill all ..."

# run
for f in ${frameworks[@]}; do
    . ./script/killone.sh "${f}.server"
done
. ./script/killone.sh "bench.client"

echo "kill all done"
