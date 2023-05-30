#!/bin/bash

. ./script/util.sh

# start all servers together, else it would hard to bind addr and start failed after some benchmark
for f in ${frameworks[@]}; do
    echo
    ./script/server.sh $f
done

