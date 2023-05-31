#!/bin/bash

. ./script/env.sh

for f in ${frameworks[@]}; do
    # echo "start bench ${f}" $1 $2 $3 $4 $5 $6 $7 $8 $9
    echo
    #spid=$(ps ux | grep ${f}.server | awk '{split($2,a," ");print a[1] "xx" a[2]}')
    spid=$(pidof ${f}.server)
    ./script/client.sh -f=$f -spid=$spid $1 $2 $3 $4 $5 $6 $7 $8 $9
done

