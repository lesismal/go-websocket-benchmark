# go-websocket-benchmark
- support 1m-connections client
- support 1m-connections nbio_mod_nonblocking server

## Benchmark
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
processors:

processor	: 0
processor	: 1
processor	: 2
processor	: 3
processor	: 4
processor	: 5
processor	: 6
processor	: 7
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

