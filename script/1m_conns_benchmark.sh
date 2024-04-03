#!/bin/bash

. ./script/env.sh

echo $line

. ./script/killall.sh

echo $line

. ./script/clean.sh

echo $line

frameworks=(
    "greatws_event"
    "greatws"
    "nbio_nonblocking"
)

print_env

echo $line

. ./script/build.sh

echo $line

# $1 nodelay
. ./script/servers.sh $1

echo $line

sleep 3

. ./script/clients.sh -c=1000000 -en=2000000 -b=1024 -rr=1

# echo $line

. ./script/report.sh $1 $2 $3 $4 $5 $6 $7 $8 $9

echo $line
