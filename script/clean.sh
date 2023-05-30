#!/bin/bash

. ./script/util.sh

echo "clean ..."

rm -rf ./output

# run
for f in ${frameworks[@]}; do
    killall -9 "${f}.server" 1>/dev/null 2>&1
done

echo "clean done"
