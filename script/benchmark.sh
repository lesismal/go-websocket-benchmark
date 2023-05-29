#!/bin/bash

. ./script/util.sh

echo $line

. ./script/build.sh

echo $line

. ./script/servers.sh

echo $line

. ./script/clients.sh

echo $line
