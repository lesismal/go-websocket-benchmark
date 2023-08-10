# go-websocket-benchmark
- support 1m-connections client

## before running the test
- make sure setting the correct system env, for example:

```sh
sysctl -w net.ipv4.ip_local_port_range="1024 65535"
sysctl -w fs.file-max=2000500
sysctl -w fs.nr_open=2000500
sysctl -w net.nf_conntrack_max=2000500
ulimit -n 2000500
sysctl -w net.ipv4.tcp_mem='131072  262144  524288'
sysctl -w net.ipv4.tcp_rmem='8760  256960  4088000'
sysctl -w net.ipv4.tcp_wmem='8760  256960  4088000'
sysctl -w net.core.rmem_max=16384
sysctl -w net.core.wmem_max=16384
sysctl -w net.core.somaxconn=2048
sysctl -w net.ipv4.tcp_max_syn_backlog=2048
sysctl -w /proc/sys/net/core/netdev_max_backlog=2048
# sysctl -w net.ipv4.tcp_tw_recycle=1 # client nat tcp-handshak problem
sysctl -w net.ipv4.tcp_tw_reuse=1
```

## nbio 1m-connections-benchmark


run:
```sh
git clone https://github.com/lesismal/go-websocket-benchmark.git
cd go-websocket-benchmark
./script/1m_conns_benchmark.sh
```

here is the result on my ubuntu vm:
```sh
--------------------------------------------------------------
BenchType  : Connections
Framework  : nbio_nonblocking
Connections: 1000000
Concurrency: 5000
Success    : 1000000
Failed     : 0
Used       : 41.56s
TPS        : 24061
Min        : 20ns
Avg        : 192.57ms
Max        : 41.52s
TP50       : 30ns
TP75       : 30ns
TP90       : 30ns
TP95       : 31ns
TP99       : 31ns
--------------------------------------------------------------
BenchType  : BenchEcho
Framework  : nbio_nonblocking
Conns      : 1000000
Concurrency: 50000
Payload    : 1024
Total      : 5000000
Success    : 5000000
Failed     : 0
Used       : 47.02s
CPU Min    : 0.00%
CPU Avg    : 340.08%
CPU Max    : 386.93%
MEM Min    : 1.76G
MEM Avg    : 1.91G
MEM Max    : 1.94G
TPS        : 106348
Min        : 436.16us
Avg        : 465.78ms
Max        : 2.42s
TP50       : 412.36ms
TP75       : 600.92ms
TP90       : 779.92ms
TP95       : 1.04s
TP99       : 1.35s
--------------------------------------------------------------------------
```

## benchmark for all frameworks
run:
```sh
git clone https://github.com/lesismal/go-websocket-benchmark.git
cd go-websocket-benchmark
./script/benchmarkN.sh

# if you want to change the benchmark config, just read the script and edit:
# go-websocket-benchmark/script/config.sh
```

Some benchmark results:

20230804 22:01.04.584 [BenchEcho] Report

|    Framework     |  TPS   |   Min   |   Avg   |   Max    |  TP50   |  TP75   |  TP90   |  TP95   |  TP99   | Used  |  Total  | Success | Failed | Conns | Concurrency | Payload | CPU Min | CPU Avg | CPU Max | MEM Min | MEM Avg | MEM Max |
|     ---          |  ---   |   ---   |   ---   |   ---    |   ---   |   ---   |   ---   |   ---   |   ---   |  ---  |   ---   |   ---   |  ---   |  ---  |     ---     |   ---   |   ---   |   ---   |   ---   |   ---   |   ---   |   ---   |
|   fasthttp       | 626359 | 17.34us | 15.91ms | 241.32ms | 14.48ms | 16.32ms | 21.12ms | 22.40ms | 25.32ms | 3.19s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 636.94  | 643.58  | 646.94  | 263.57M | 265.70M | 267.83M |
|    gobwas        | 510595 | 11.39us | 19.49ms | 251.97ms | 16.62ms | 21.06ms | 26.69ms | 34.10ms | 77.68ms | 3.92s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 718.84  | 762.87  | 785.80  | 361.89M | 364.79M | 366.24M |
|    gorilla       | 620387 | 13.78us | 16.05ms | 235.99ms | 14.44ms | 16.35ms | 21.53ms | 23.15ms | 36.00ms | 3.22s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 640.17  | 646.90  | 652.78  | 262.93M | 264.93M | 266.93M |
|     gws          | 640529 | 7.32us  | 15.55ms | 140.41ms | 13.54ms | 15.48ms | 21.49ms | 23.01ms | 70.18ms | 3.12s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 638.90  | 641.26  | 643.94  | 170.94M | 171.40M | 171.86M |
|    gws_std       | 628836 | 12.49us | 15.86ms | 246.86ms | 14.01ms | 16.15ms | 21.77ms | 23.23ms | 46.66ms | 3.18s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 635.95  | 642.24  | 647.87  | 318.72M | 331.50M | 344.29M |
|    hertz         | 270051 | 10.24ms | 36.90ms | 80.09ms  | 33.25ms | 35.15ms | 61.32ms | 63.22ms | 65.65ms | 7.41s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 382.97  | 390.58  | 394.66  | 509.59M | 552.77M | 595.20M |
|   hertz_std      | 604316 | 17.63us | 16.49ms | 233.92ms | 14.81ms | 17.34ms | 22.42ms | 23.76ms | 29.31ms | 3.31s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 674.94  | 687.22  | 697.85  | 332.12M | 334.19M | 336.27M |
|  nbio_blocking   | 646592 | 12.83us | 15.39ms | 226.96ms | 13.84ms | 15.71ms | 21.18ms | 22.53ms | 25.48ms | 3.09s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 635.73  | 652.49  | 661.84  | 167.41M | 180.08M | 192.76M |
|   nbio_mixed     | 647891 | 11.41us | 15.34ms | 244.91ms | 13.64ms | 15.74ms | 20.88ms | 22.23ms | 47.71ms | 3.09s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 629.76  | 637.21  | 647.92  | 224.78M | 225.82M | 226.86M |
| nbio_nonblocking | 484499 | 18.48us | 20.55ms | 135.01ms | 18.82ms | 25.85ms | 33.46ms | 39.58ms | 52.92ms | 4.13s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 669.95  | 681.43  | 691.97  | 121.20M | 122.72M | 123.88M |
|   nbio_std       | 594338 | 8.58us  | 16.78ms | 108.26ms | 14.82ms | 18.63ms | 23.01ms | 26.32ms | 55.82ms | 3.37s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 643.88  | 665.11  | 680.91  | 172.60M | 175.74M | 178.88M |
|    nettyws       | 637288 | 10.18us | 15.63ms | 105.14ms | 14.02ms | 16.06ms | 20.97ms | 22.50ms | 35.77ms | 3.14s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 624.95  | 636.00  | 643.86  | 173.52M | 178.02M | 182.52M |
|    nhooyr        | 478661 | 10.65us | 20.80ms | 105.04ms | 19.23ms | 21.55ms | 28.03ms | 31.75ms | 59.06ms | 4.18s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 789.93  | 797.15  | 799.94  | 374.30M | 381.30M | 389.00M |
|    quickws       | 658489 | 10.21us | 15.13ms | 108.65ms | 13.58ms | 15.63ms | 20.14ms | 21.46ms | 33.63ms | 3.04s | 2000000 | 2000000 |   0    | 10000 |    10000    |  1024   | 598.95  | 601.89  | 606.81  | 121.34M | 121.34M | 121.34M |

20230804 22:01.04.603 [BenchRate] Report

|    Framework     | Duration | Packet Sent | Bytes Sent | Packet Recv | Bytes Recv | Conns | SendRate | Payload | CPU Min | CPU Avg | CPU Max | MEM Min | MEM Avg | MEM Max |
|     ---          |   ---    |     ---     |    ---     |     ---     |    ---     |  ---  |   ---    |   ---   |   ---   |   ---   |   ---   |   ---   |   ---   |   ---   |
|   fasthttp       |  10.00s  |  19716970   |   18.80G   |  19716970   |   18.80G   | 10000 |   200    |  1024   | 755.06  | 772.10  | 786.86  | 282.74M | 335.02M | 397.34M |
|    gobwas        |  10.00s  |   9390220   |   8.96G    |   9138368   |   8.72G    | 10000 |   200    |  1024   | 746.92  | 757.94  | 769.85  | 440.77M | 477.40M | 513.70M |
|    gorilla       |  10.00s  |  19832610   |   18.91G   |  19832610   |   18.91G   | 10000 |   200    |  1024   | 751.19  | 765.83  | 784.49  | 300.57M | 351.94M | 378.28M |
|     gws          |  10.00s  |  19693400   |   18.78G   |  19693400   |   18.78G   | 10000 |   200    |  1024   | 763.46  | 785.61  | 794.74  | 191.07M | 197.97M | 202.86M |
|    gws_std       |  10.00s  |  19819030   |   18.90G   |  19819030   |   18.90G   | 10000 |   200    |  1024   | 733.12  | 755.77  | 774.18  | 368.39M | 371.17M | 372.80M |
|    hertz         |  10.00s  |  12542490   |   11.96G   |  12211819   |   11.65G   | 10000 |   200    |  1024   | 639.92  | 650.38  | 688.13  | 662.18M | 746.45M | 824.33M |
|   hertz_std      |  10.00s  |  19837530   |   18.92G   |  19837530   |   18.92G   | 10000 |   200    |  1024   | 744.39  | 793.03  | 801.84  | 371.93M | 435.85M | 499.13M |
|  nbio_blocking   |  10.00s  |  19711560   |   18.80G   |  19711560   |   18.80G   | 10000 |   200    |  1024   | 731.78  | 776.91  | 787.87  | 207.99M | 208.55M | 208.62M |
|   nbio_mixed     |  10.00s  |  19857130   |   18.94G   |  19857130   |   18.94G   | 10000 |   200    |  1024   | 751.46  | 771.90  | 787.79  | 344.78M | 414.50M | 433.02M |
| nbio_nonblocking |  10.00s  |  17597410   |   16.78G   |  17489228   |   16.68G   | 10000 |   200    |  1024   | 748.09  | 756.91  | 763.91  | 457.25M | 557.19M | 583.99M |
|   nbio_std       |  10.00s  |  19899870   |   18.98G   |  19840008   |   18.92G   | 10000 |   200    |  1024   | 755.10  | 766.49  | 783.42  | 196.35M | 196.37M | 196.47M |
|    nettyws       |  10.00s  |  19569210   |   18.66G   |  19525710   |   18.62G   | 10000 |   200    |  1024   | 757.65  | 790.72  | 801.54  | 228.10M | 241.45M | 244.84M |
|    nhooyr        |  10.00s  |  10424290   |   9.94G    |  10424290   |   9.94G    | 10000 |   200    |  1024   | 742.93  | 793.16  | 799.63  | 422.39M | 472.55M | 495.47M |
|    quickws       |  10.00s  |  19898080   |   18.98G   |  19898080   |   18.98G   | 10000 |   200    |  1024   | 722.18  | 731.32  | 738.90  | 139.14M | 142.43M | 142.94M |
----------------------------------------------------------------------------------------------------