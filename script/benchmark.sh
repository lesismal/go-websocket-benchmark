#!/bin/bash

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

echo $line

. ./script/clients.sh -rate=true $1 $2 $3 $4 $5 $6 $7 $8 $9

# echo $line

. ./script/report.sh $1 $2 $3 $4 $5 $6 $7 $8 $9

echo $line
