#!/bin/bash

. ./script/config.sh

# Connections=(10000 50000 100000)
# BodySize=(128 512 1024 4096)
# BenchTime=(2000000)

. ./script/env.sh || { return 1 2>/dev/null || exit 1; }

echo $line
. ./script/clean.sh

echo $line

print_env

echo $line

. ./script/build.sh || { return 1 2>/dev/null || exit 1; }

echo $line

. ./script/killall.sh
sleep 1
. ./script/servers.sh $1
sleep 3
echo $line
for f in ${frameworks[@]}; do
    echo "run ${f} server on cpu ${server_cpu_list:-unbound}"
    # nohup $limit_cpu_server "./output/bin/${f}.server" -b=$b >"./output/log/${f}${suffix}.log" 2>&1 &
    for c in ${Connections[@]}; do
        for b in ${BodySize[@]}; do
            for n in ${BenchTime[@]}; do
                # echo $line
                suffix="_${c}_${b}_${n}"
                #echo "benchmarkN: [${f}], ${c} connections, ${b} payload, ${n} times"
                . ./script/client.sh -f=$f -c=$c -b=$b -en=$n -suffix=${suffix} -rate=true || { return 1 2>/dev/null || exit 1; }
                sleep $SleepTime
            done
        done
    done
    . ./script/killone.sh "${f}.server"
done

for c in ${Connections[@]}; do
    for b in ${BodySize[@]}; do
        for n in ${BenchTime[@]}; do
            # echo $line
            suffix="_${c}_${b}_${n}"
            . ./script/report.sh -suffix=${suffix} "$@"
        done
    done
done
# echo $line
