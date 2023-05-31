#!/bin/bash

. ./script/config.sh

# Connections=(10000 50000 100000)
# BodySize=(128 512 1024 4096)
# BenchTime=(2000000)

. ./script/env.sh

echo $line
. ./script/clean.sh

echo $line

print_env

echo $line

. ./script/build.sh

echo $line

for c in ${Connections[@]}; do
    for b in ${BodySize[@]}; do
        for n in ${BenchTime[@]}; do
            suffix="_${c}_${b}_${n}"
            echo $line
            . ./script/killall.sh

            echo $line

            for f in ${frameworks[@]}; do
                echo
                echo "run ${f} server on cpu 0-${server_cpu_num}"
                nohup $limit_cpu_server "./output/bin/${f}.server" -b=$b >"./output/log/${f}${suffix}.log" 2>&1 &
            done

            echo $line

            echo "benchmarkN: ${c} connections, ${b} payload, ${n} times"
            . ./script/clients.sh -c=$c -b=$b -n=$n -suffix=${suffix}
            echo $line
            . ./script/report.sh -r=true -suffix=${suffix} $1 $2 $3 $4 $5 $6 $7 $8 $9

            sleep $SleepTime
        done
    done
done
echo $line
