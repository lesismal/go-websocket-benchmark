#!/bin/bash

. ./script/env.sh

for f in ${frameworks[@]}; do
    # echo "start bench ${f}" $1 $2 $3 $4 $5 $6 $7 $8 $9
    echo
    #spid=$(pidof ${f}.server)
    #./script/client.sh -f=$f -spid=$spid $1 $2 $3 $4 $5 $6 $7 $8 $9
    ./script/client.sh -f=$f $1 $2 $3 $4 $5 $6 $7 $8 $9
    . ./script/killone.sh "${f}.server"
    sleep $SleepTime
done
