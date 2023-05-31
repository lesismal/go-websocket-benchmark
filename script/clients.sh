#!/bin/bash

. ./script/env.sh

for f in ${frameworks[@]}; do
    # echo "start bench ${f}" $1 $2 $3 $4 $5 $6 $7 $8 $9
    echo
    ./script/client.sh -f="${f}" $1 $2 $3 $4 $5 $6 $7 $8 $9
done
