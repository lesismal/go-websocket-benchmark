#!/bin/bash

# . ./script/env.sh
# . ./script/config.sh

# start all servers together, else it would hard to bind addr and start failed after some benchmark
for f in ${frameworks[@]}; do
    echo
    # $1 nodelay
    ./script/server.sh $f $1
done
