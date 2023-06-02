# Go-Websocket-Benchmark
- support 1m-connections client
- support 1m-connections nbio_nonblocking server

## 1M-Connections-Benchmark For nbio
- Run
```sh
git clone https://github.com/lesismal/go-websocket-benchmark.git
cd go-websocket-benchmark

# build
./script/build.sh

# start server
./script/server.sh nbio_nonblocking 
# or
# ./script/server.sh nbio_mixed

# start benchmark client
# -c connections
# -n benchmark times
./script/client.sh -f=nbio_nonblocking -c=1000000 -n=5000000 -b=1024
# or 
# ./script/client.sh -f=nbio_mixed -c=1000000 -n=5000000 -b=1024
```

Here is some 1M-Connections-Benchmark report on my ubuntu vm, the nbio non-blocking server use cpu 0-3 and SetMemoryLimit(2G), benchmark with 1k payload:
```sh
root@ubuntu:~/go-websocket-benchmark# ./script/client.sh -f=nbio_nonblocking -c=1000000 -n=5000000 -b=1024
2023/05/30 16:04:51.048 [INF] NBIO[Benchmark-Client] start
2023/05/30 16:04:51 1000000 clients start connecting
2023/05/30 16:04:52 25890 clients connected
2023/05/30 16:04:53 69455 clients connected
2023/05/30 16:04:54 116592 clients connected
......
2023/05/30 16:05:20 999998 clients connected
2023/05/30 16:05:21 999998 clients connected
2023/05/30 16:05:21 1000000 clients connected
-------------------------
Benchmark: nbio_nonblocking
Conns    : 1000000
Payload  : 1024
TOTAL    : 5000000 times
SUCCESS  : 5000000, 100.00%
FAILED   : 0, 0.00%
TPS      : 100672
TIME USED: 49.67s
MIN USED : 39.24us
AVG USED : 19.86ms
MAX USED : 242.16ms
TP50     : 18.56ms
TP75     : 26.04ms
TP90     : 34.24ms
TP95     : 39.64ms
TP99     : 52.16ms
CPU MIN  : 0.00%
CPU AVG  : 293.65%
CPU MAX  : 319.95%
MEM MIN  : 1.61G
MEM AVG  : 1.91G
MEM MAX  : 2.06G
-------------------------
```

Or just run:
```sh
./script/1m_conns_benchmark.sh
```

- Clean
```sh
./script/clean.sh
```

## Benchmark For All Frameworks
- Run
```sh
git clone https://github.com/lesismal/go-websocket-benchmark.git
cd go-websocket-benchmark
./script/benchmark.sh
```

- Clean
```sh
./script/clean.sh
```

Some benchmark result on my ubuntu vm:
```sh
--------------------------------
os:

Ubuntu 20.04.6 LTS \n \l

--------------------------------
cpu model:

model name	: AMD Ryzen 7 5800H with Radeon Graphics
--------------------------------
              total        used        free      shared  buff/cache   available
Mem:       16362568      396988    15151676        1636      813904    15656380
Swap:             0           0           0
--------------------------------
```

|      Framework       | Conns |  Total  | Success | Failed | Used  | CPU Avg | MEM Avg |   Avg   |  TPS   |  TP50   |  TP90   |  TP99   |
|      ---             |  ---  |   ---   |   ---   |  ---   |  ---  |   ---   |   ---   |   ---   |  ---   |   ---   |   ---   |   ---   |
|     gobwas           | 10000 | 1000000 | 1000000 |   0    | 6.70s | 386.29% | 90.04M  | 13.38ms | 149157 | 8.84ms  | 29.87ms | 72.26ms |
|     gorilla          | 10000 | 1000000 | 1000000 |   0    | 4.48s | 286.82% | 257.43M | 8.95ms  | 223121 | 7.80ms  | 15.78ms | 26.51ms |
|      gws             | 10000 | 1000000 | 1000000 |   0    | 4.23s | 248.67% | 160.78M | 8.46ms  | 236251 | 7.38ms  | 14.70ms | 25.91ms |
| gws_std  | 10000 | 1000000 | 1000000 |   0    | 4.36s | 267.13% | 264.81M | 8.71ms  | 229483 | 7.65ms  | 15.24ms | 25.59ms |
| nbio_std | 10000 | 1000000 | 1000000 |   0    | 4.36s | 280.86% | 198.42M | 8.71ms  | 229348 | 7.61ms  | 15.37ms | 25.51ms |
|  nbio_blocking   | 10000 | 1000000 | 1000000 |   0    | 4.57s | 310.62% | 185.31M | 9.13ms  | 218737 | 7.82ms  | 16.34ms | 28.02ms |
|   nbio_mixed     | 10000 | 1000000 | 1000000 |   0    | 4.66s | 312.51% | 204.19M | 9.30ms  | 214786 | 8.03ms  | 16.47ms | 28.73ms |
| nbio_nonblocking | 10000 | 1000000 | 1000000 |   0    | 5.14s | 292.00% | 86.74M  | 10.27ms | 194373 | 9.37ms  | 16.94ms | 26.30ms |
|     nhooyr           | 10000 | 1000000 | 1000000 |   0    | 6.60s | 405.59% | 565.03M | 13.18ms | 151436 | 11.17ms | 22.42ms | 47.47ms |

## TODO
1. Add cpu/mem count to the result - done
2. Auto save Charts/Markdown/Xls files - Markdown done
3. Different Args
4. Add more frameworks


