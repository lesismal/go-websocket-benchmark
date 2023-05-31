#!/bin/bash

. ./script/config.sh

# Connections=(10000 50000 100000)
# BodySize=(128 512 1024 4096)
# BenchTime=(2000000)

. ./script/env.sh

echo $line
. ./script/killall.sh

echo $line
. ./script/clean.sh

echo $line

print_env

echo $line

. ./script/build.sh

echo $line

. ./script/servers.sh

for c in ${Connections[@]}; do
    for b in ${BodySize[@]}; do
        for n in ${BenchTime[@]}; do
            echo $line
            echo "benchmarkN: ${c} connections, ${b} payload, ${n} times"
            . ./script/clients.sh -c=$c -b=$b -n=$n -suffix="_${c}_${b}_${n}"
            echo $line
            . ./script/report.sh -r=true -suffix="_${c}_${b}_${n}" $1 $2 $3 $4 $5 $6 $7 $8 $9
            sleep 5
        done
    done
done
echo $line
