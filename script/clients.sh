#!/bin/bash

for f in ${frameworks[@]}; do
    echo "start bench ${f}"
    echo
    #./output/bin/bench.client -f="${f}" -c=50000 -n=2000000
    ./script/client.sh -f="${f}" -c=10000 -n=1000000 $1 $2 $3 $4 $5
done
