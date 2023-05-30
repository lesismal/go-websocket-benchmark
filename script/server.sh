#!/bin/bash

nohup $limit_cpu_server "./output/bin/${1}.server" $2 $3 $4 $5 $6 $7 $8 $9 >"./output/log/${1}.log" 2>&1 &

