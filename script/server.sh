#!/bin/bash

nohup $limit_cpu_server "./output/bin/${1}.server" >"./output/log/${1}.log" 2>&1 &

