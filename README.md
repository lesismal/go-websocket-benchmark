# go-websocket-benchmark (support 1m-connections server and client)

## Benchmark
- Run
```sh
git clone https://github.com/lesismal/go-websocket-benchmark.git
cd go-websocket-benchmark
./script/run.sh
```

- Clean
```sh
./script/run.sh
```


Some output of `script/run.sh` on my ubuntu vm:
```sh
root@ubuntu:~/go-websocket-benchmark# ./script/run.sh 
building...
build done
run each server on cpu 0-3
2023/05/29 08:43:40.706 [INF] NBHTTP[NB] Start with "IOModNonBlocking"
2023/05/29 08:43:40.706 [INF] NBIO[NB] start
2023/05/29 08:43:40 50000 clients start connecting
2023/05/29 08:43:41 33074 clients connected
2023/05/29 08:43:42 50000 clients connected
-------------------------
NAME     : gobwas
BENCHMARK: 2000000 times
SUCCESS  : 2000000, 100.00%
FAILED   : 0, 0.00%
TPS      : 152015
TIME USED: 13.16s
MIN USED : 0.04ms
MAX USED : 175.41ms
AVG USED : 13.15ms
TP50     : 9.46ms
TP75     : 14.76ms
TP90     : 26.37ms
TP95     : 38.25ms
TP99     : 65.37ms
-------------------------
2023/05/29 08:43:57.241 [INF] NBHTTP[NB] Start with "IOModNonBlocking"
2023/05/29 08:43:57.241 [INF] NBIO[NB] start
2023/05/29 08:43:57 50000 clients start connecting
2023/05/29 08:43:58 34339 clients connected
2023/05/29 08:43:58 50000 clients connected
-------------------------
NAME     : gorilla
BENCHMARK: 2000000 times
SUCCESS  : 2000000, 100.00%
FAILED   : 0, 0.00%
TPS      : 219052
TIME USED: 9.13s
MIN USED : 0.03ms
MAX USED : 56.69ms
AVG USED : 9.13ms
TP50     : 7.81ms
TP75     : 11.67ms
TP90     : 16.31ms
TP95     : 20.06ms
TP99     : 29.85ms
-------------------------
2023/05/29 08:44:09.338 [INF] NBHTTP[NB] Start with "IOModNonBlocking"
2023/05/29 08:44:09.339 [INF] NBIO[NB] start
2023/05/29 08:44:09 50000 clients start connecting
2023/05/29 08:44:10 38585 clients connected
2023/05/29 08:44:10 50000 clients connected
-------------------------
NAME     : gws
BENCHMARK: 2000000 times
SUCCESS  : 2000000, 100.00%
FAILED   : 0, 0.00%
TPS      : 231702
TIME USED: 8.63s
MIN USED : 0.02ms
MAX USED : 67.84ms
AVG USED : 8.63ms
TP50     : 7.31ms
TP75     : 10.94ms
TP90     : 15.46ms
TP95     : 18.90ms
TP99     : 29.30ms
-------------------------
2023/05/29 08:44:20.739 [INF] NBHTTP[NB] Start with "IOModNonBlocking"
2023/05/29 08:44:20.739 [INF] NBIO[NB] start
2023/05/29 08:44:20 50000 clients start connecting
2023/05/29 08:44:21 31381 clients connected
2023/05/29 08:44:22 50000 clients connected
-------------------------
NAME     : gws_basedon_stdhttp
BENCHMARK: 2000000 times
SUCCESS  : 2000000, 100.00%
FAILED   : 0, 0.00%
TPS      : 223782
TIME USED: 8.94s
MIN USED : 0.03ms
MAX USED : 72.31ms
AVG USED : 8.93ms
TP50     : 7.60ms
TP75     : 11.40ms
TP90     : 15.96ms
TP95     : 19.40ms
TP99     : 29.55ms
-------------------------
2023/05/29 08:44:32.677 [INF] NBHTTP[NB] Start with "IOModNonBlocking"
2023/05/29 08:44:32.677 [INF] NBIO[NB] start
2023/05/29 08:44:32 50000 clients start connecting
2023/05/29 08:44:33 33968 clients connected
2023/05/29 08:44:34 50000 clients connected
-------------------------
NAME     : nbio_basedon_stdhttp
BENCHMARK: 2000000 times
SUCCESS  : 2000000, 100.00%
FAILED   : 0, 0.00%
TPS      : 219840
TIME USED: 9.10s
MIN USED : 0.03ms
MAX USED : 63.30ms
AVG USED : 9.09ms
TP50     : 7.70ms
TP75     : 11.51ms
TP90     : 16.49ms
TP95     : 20.38ms
TP99     : 29.96ms
-------------------------
2023/05/29 08:44:44.794 [INF] NBHTTP[NB] Start with "IOModNonBlocking"
2023/05/29 08:44:44.794 [INF] NBIO[NB] start
2023/05/29 08:44:44 50000 clients start connecting
2023/05/29 08:44:45 35579 clients connected
2023/05/29 08:44:46 50000 clients connected
-------------------------
NAME     : nbio_mod_blocking
BENCHMARK: 2000000 times
SUCCESS  : 2000000, 100.00%
FAILED   : 0, 0.00%
TPS      : 217891
TIME USED: 9.18s
MIN USED : 0.02ms
MAX USED : 79.69ms
AVG USED : 9.17ms
TP50     : 7.69ms
TP75     : 11.53ms
TP90     : 16.64ms
TP95     : 20.85ms
TP99     : 31.57ms
-------------------------
2023/05/29 08:44:56.839 [INF] NBHTTP[NB] Start with "IOModNonBlocking"
2023/05/29 08:44:56.839 [INF] NBIO[NB] start
2023/05/29 08:44:56 50000 clients start connecting
2023/05/29 08:44:57 35217 clients connected
2023/05/29 08:44:58 50000 clients connected
-------------------------
NAME     : nbio_mod_mixed
BENCHMARK: 2000000 times
SUCCESS  : 2000000, 100.00%
FAILED   : 0, 0.00%
TPS      : 194469
TIME USED: 10.28s
MIN USED : 0.04ms
MAX USED : 89.96ms
AVG USED : 10.28ms
TP50     : 9.13ms
TP75     : 12.78ms
TP90     : 17.33ms
TP95     : 20.94ms
TP99     : 32.18ms
-------------------------
2023/05/29 08:45:10.404 [INF] NBHTTP[NB] Start with "IOModNonBlocking"
2023/05/29 08:45:10.404 [INF] NBIO[NB] start
2023/05/29 08:45:10 50000 clients start connecting
2023/05/29 08:45:11 33101 clients connected
2023/05/29 08:45:11 50000 clients connected
-------------------------
NAME     : nbio_mod_nonblocking
BENCHMARK: 2000000 times
SUCCESS  : 2000000, 100.00%
FAILED   : 0, 0.00%
TPS      : 187462
TIME USED: 10.67s
MIN USED : 0.04ms
MAX USED : 85.60ms
AVG USED : 10.66ms
TP50     : 9.41ms
TP75     : 13.19ms
TP90     : 17.83ms
TP95     : 21.56ms
TP99     : 34.04ms
-------------------------
2023/05/29 08:45:24.523 [INF] NBHTTP[NB] Start with "IOModNonBlocking"
2023/05/29 08:45:24.523 [INF] NBIO[NB] start
2023/05/29 08:45:24 50000 clients start connecting
2023/05/29 08:45:25 27039 clients connected
2023/05/29 08:45:26 50000 clients connected
-------------------------
NAME     : nhooyr
BENCHMARK: 2000000 times
SUCCESS  : 2000000, 100.00%
FAILED   : 0, 0.00%
TPS      : 155113
TIME USED: 12.89s
MIN USED : 0.05ms
MAX USED : 138.47ms
AVG USED : 12.88ms
TP50     : 10.94ms
TP75     : 14.85ms
TP90     : 21.92ms
TP95     : 29.03ms
TP99     : 45.47ms
-------------------------
```

## TODO
1. Add cpu/mem count to the result
2. Auto save Charts/Markdown/Xls files