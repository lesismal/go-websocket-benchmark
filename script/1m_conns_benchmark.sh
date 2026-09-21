#!/bin/bash

. ./script/env.sh || { return 1 2>/dev/null || exit 1; }

echo $line

. ./script/killall.sh

echo $line

. ./script/clean.sh

echo $line

frameworks=(
    "greatws_event"
    "greatws"
    "nbio_nonblocking"
    "uws_events"
    "fnet"
)

print_env

echo $line

. ./script/build.sh || { return 1 2>/dev/null || exit 1; }

echo $line

# $1 nodelay
. ./script/servers.sh $1

echo $line

sleep 3

. ./script/clients.sh -c=1000000 -en=2000000 -b=1024 -rr=1 || { return 1 2>/dev/null || exit 1; }

# echo $line

. ./script/report.sh "$@"

echo $line
