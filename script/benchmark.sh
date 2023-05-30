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

. ./script/clients.sh -suffix="_x"

echo $line

. ./script/report.sh -r=true -suffix="_x"

echo $line
