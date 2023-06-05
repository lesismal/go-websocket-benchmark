#!/bin/bash

. ./script/env.sh

killcmd=pkill
if [ $(which killall) ]; then
    killcmd=killall
fi

echo "kill ${1} ..."
# $killcmd -2 "${1}" 1>/dev/null 2>&1
$killcmd -2 "${1}"
