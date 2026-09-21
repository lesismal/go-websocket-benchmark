#!/bin/bash

echo "generate report ..."
echo
./output/bin/bench.client -r=true "$@"
echo
echo "generate report done"

