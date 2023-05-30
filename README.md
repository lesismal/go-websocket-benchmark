# Go-Websocket-Benchmark
- support 1m-connections client
- support 1m-connections nbio_mod_nonblocking server

## 1M-Connections-Benchmark For nbio
- Run
```sh
git clone https://github.com/lesismal/go-websocket-benchmark.git
cd go-websocket-benchmark

# build
./script/build.sh

# start server
./script/server.sh nbio_mod_nonblocking 
# or
# ./script/server.sh nbio_mod_mixed

# start benchmark client
# -c connections
# -n benchmark times
./script/client.sh -f=nbio_mod_nonblocking -c=1000000 -n=5000000 -b=1024
# or 
# ./script/client.sh -f=nbio_mod_nonblocking -c=1000000 -n=5000000 -b=1024
```

Some benchmark result on my ubuntu vm:
```sh
2023/05/30 14:27:01.747 [INF] NBIO[Benchmark-Client] start
2023/05/30 14:27:01 1000000 clients start connecting
2023/05/30 14:27:02 29006 clients connected
2023/05/30 14:27:03 65988 clients connected
......
2023/05/30 14:27:33 999988 clients connected
2023/05/30 14:27:33 1000000 clients connected
-------------------------
BENCHMARK: nbio_mod_nonblocking
TOTAL    : 5000000 times
SUCCESS  : 5000000, 100.00%
FAILED   : 0, 0.00%
TPS      : 126575
TIME USED: 39.50s
MIN USED : 59.06ms
MAX USED : 237.44us
AVG USED : 15.80us
TP50     : 14.35us
TP75     : 19.70us
TP90     : 25.88us
TP95     : 30.52us
TP99     : 42.36us
CPU MIN  : 0.00%
CPU MIN  : 365.01%
CPU MIN  : 430.50%
MEM MIN  : 1.60G
MEM MIN  : 1.95G
MEM MIN  : 2.09G
-------------------------
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

|      Framework       |  Total  | Success | Failed | Used  | CPU Avg | MEM Avg |   Avg   |  TPS   |  TP50   |  TP90   |  TP99   |
|      ---             |   ---   |   ---   |  ---   |  ---  |   ---   |   ---   |   ---   |  ---   |   ---   |   ---   |   ---   |
|     gobwas           | 1000000 | 1000000 |   0    | 6.80s | 396.71% | 91.11M  | 13.59us | 146985 | 8.63us  | 31.09us | 73.68us |
|     gorilla          | 1000000 | 1000000 |   0    | 4.92s | 258.53% | 255.65M | 9.83us  | 203196 | 8.55us  | 17.30us | 29.85us |
|      gws             | 1000000 | 1000000 |   0    | 4.42s | 259.79% | 142.47M | 8.84us  | 226012 | 7.80us  | 15.43us | 25.08us |
| gws_basedon_stdhttp  | 1000000 | 1000000 |   0    | 4.17s | 249.58% | 270.57M | 8.33us  | 239920 | 7.33us  | 14.58us | 23.88us |
| nbio_basedon_stdhttp | 1000000 | 1000000 |   0    | 4.56s | 303.26% | 201.21M | 9.10us  | 219425 | 7.77us  | 16.31us | 28.07us |
|  nbio_mod_blocking   | 1000000 | 1000000 |   0    | 4.86s | 330.15% | 182.15M | 9.71us  | 205733 | 8.24us  | 17.61us | 30.21us |
|   nbio_mod_mixed     | 1000000 | 1000000 |   0    | 4.86s | 332.80% | 185.00M | 9.70us  | 205897 | 8.27us  | 17.51us | 29.93us |
| nbio_mod_nonblocking | 1000000 | 1000000 |   0    | 5.03s | 285.31% | 86.80M  | 10.04us | 198945 | 9.16us  | 16.52us | 26.20us |
|     nhooyr           | 1000000 | 1000000 |   0    | 6.52s | 396.90% | 567.91M | 13.02us | 153434 | 10.89us | 21.99us | 48.62us |

## TODO
1. Add cpu/mem count to the result - done
2. Auto save Charts/Markdown/Xls files - Markdown done
3. Different Args
4. Add more frameworks

