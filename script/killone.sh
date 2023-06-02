#!/bin/bash

. ./script/env.sh

echo "kill ${1} ..."
# $killcmd -9 "${1}" 1>/dev/null 2>&1
$killcmd -9 "${1}"
