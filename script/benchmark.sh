#!/bin/bash

. ./script/util.sh

echo $line

. ./script/clean.sh

echo $line

. ./script/env.sh

echo $line

. ./script/build.sh

echo $line

. ./script/servers.sh

echo $line

. ./script/clients.sh

echo $line

. ./script/report.sh -r=true

echo $line
