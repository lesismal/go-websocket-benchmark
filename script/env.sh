#!/bin/bash

. ./script/util.sh

echo "os:"
echo
cat /etc/issue
echo $line
echo "cpu model:"
echo
cat /proc/cpuinfo | grep "model name" | uniq
echo $line
echo "processors:"
echo
cat /proc/cpuinfo | grep processor
echo $line
free
echo $line
echo "go env:"
echo
go env

