#!/bin/bash

. ./script/env.sh

killcmd=pkill
if [ $(which killall) ]; then
    killcmd=killall
fi

echo "kill ${1} ..."
# $killcmd -2 "${1}" 1>/dev/null 2>&1
if [ "$killcmd" = "pkill" ]; then
    # Linux limits process-name matching to 15 characters. Match the full
    # executable command line so long framework names still receive SIGINT and
    # can flush their final statistics before exiting.
    $killcmd -2 -f "[/]output/bin/${1}([[:space:]]|$)"
else
    $killcmd -2 "${1}"
fi
