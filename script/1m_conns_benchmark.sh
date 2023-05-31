#!/bin/bash

. ./script/env.sh

echo $line

. ./script/killall.sh

echo $line

. ./script/clean.sh

echo $line

. ./script/env.sh

echo $line

. ./script/build.sh

echo $line

. ./script/server.sh nbio_mod_nonblocking

echo $line

./script/client.sh -f=nbio_mod_nonblocking -c=1000000 -n=5000000 -b=1024

echo $line
