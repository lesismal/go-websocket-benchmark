#!/bin/bash

. ./script/env.sh || { return 1 2>/dev/null || exit 1; }

echo $line

. ./script/killall.sh

echo $line

. ./script/clean.sh

echo $line

print_env

echo $line

. ./script/build.sh || { return 1 2>/dev/null || exit 1; }

echo $line

# $1 nodelay
. ./script/servers.sh $1

echo $line

sleep 3

. ./script/clients.sh -rate=true "$@" || { return 1 2>/dev/null || exit 1; }

# echo $line

. ./script/report.sh "$@"

echo $line
