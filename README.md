# go-websocket-benchmark
- support 1m-connections client

## Benchmark client

`script/config.sh` selects the client with `BENCH_CLIENT`, defaulting to
`benchcli-uwscpp` (uWebSockets C++). To use the Go client:

```sh
BENCH_CLIENT=benchcli-go bash script/benchmark.sh
```

The C++ client requires a C++17 compiler, Git, Python 3 and libcurl development
headers. Its first build fetches pinned uWebSockets/uSockets and JSON dependencies.
Both clients produce `output/bin/bench.client` and compatible JSON reports.
See [C++ client build instructions, flags and tests](benchcli-uwscpp/README.md).

### Docker benchmark

The Docker runner reads the CPU set and memory exposed by the Docker daemon,
uses about 75% of its CPUs and 80% of its memory, pins separate CPU groups for
the servers and client, and copies reports, logs, console output and the selected
resource plan to `output/docker/<timestamp>`.

```sh
# Short Gorilla validation
bash script/docker_benchmark.sh --smoke

# Full benchmark using the default C++ client
bash script/docker_benchmark.sh

# Focused run with explicit resource limits
BENCH_FRAMEWORKS=gorilla,nbio_nonblocking \
DOCKER_BENCH_CPUS=8 DOCKER_BENCH_MEMORY=12g \
bash script/docker_benchmark.sh -c=10000 -en=2000000 -b=1024 -rate=true
```

Use `BENCH_CLIENT=benchcli-go` to select the Go client. Run
`bash script/docker_benchmark.sh --help` for all overrides. The first run builds
the image and downloads the pinned Go and C++ dependencies; later runs use the
Docker build cache.

## Goroutine pool

The frameworks here schedule their callbacks in different ways, and some of the
difference a benchmark shows between two of them is the pool rather than the
framework. [`taskpool`](taskpool) collects those pools behind one interface -
the fib, nbio, fnet and greatws entries import those projects' own pools, and
`uws` is the sharded executor this benchmark has always given the uws servers -
so that one pool can be run under several frameworks, or one framework under
several pools.

`script/config.sh` selects it with `BENCH_TASKPOOL`, which defaults to
`fib_adaptive`: a run nobody configured puts every framework that has a pool
hook on the one pool, and `BENCH_TASKPOOL=default` asks for the scheduling each
framework ships with instead. Two servers lose their distinguishing feature to
that default and are worth setting explicitly: `greatws_event` runs its
callbacks in the event loop only under `default` (or `inline`, which is the
same arrangement), and `uws` runs its own sharded executor only under `default`
(or `uws`).

```sh
# Every framework that can, on nbio's pool
BENCH_TASKPOOL=nbio bash script/benchmark.sh

# fib's pool, sized by hand
BENCH_TASKPOOL=fib_adaptive BENCH_TASKPOOL_MIN=500 BENCH_TASKPOOL_MAX=5000 \
BENCH_TASKPOOL_QUEUE=10000 bash script/benchmark.sh
```

| `BENCH_TASKPOOL` | pool |
| --- | --- |
| `default` | each framework's own scheduling; not a pool, and no longer what a run without the variable measures |
| `inline` | no pool: the callback runs on the I/O goroutine that read the frame |
| `go` | one goroutine per task, bounded by nothing |
| `fib_adaptive`, `fib_cond`, `fib_elastic` | `github.com/lesismal/fib/go/taskpool`, in each of its three modes (`fib_adaptive` is fib's own default, and this benchmark's) |
| `nbio` | `github.com/lesismal/nbio/taskpool` |
| `fnet` | `fnet.WorkerPool`, sharded and elastic: workers spawn on demand and retire when idle |
| `greatws` | greatws's `stream2` business pool |
| `uws` | the sharded channel executor uws runs on here |

`BENCH_TASKPOOL_MIN`, `_MAX` and `_QUEUE` are requests rather than promises: 0
leaves each pool the sizing it has in the framework it came from, and a pool
that has no use for one of them ignores it. What a worker count buys also
differs between pools - a population of parked goroutines in one, a ceiling on
forked ones in another - so the same `_MAX` does not mean the same thing to
all of them.

The servers take the same choice as `-taskpool`, `-tpmin`, `-tpmax` and
`-tpqueue`, which default the same way, and log which pool they installed. Nine of the server binaries
accept them: `fib`, `fnet`, `greatws`, `greatws_event`, `nbio_mixed`,
`nbio_nonblocking`, `uwebsockets`, `uws_events` and `uws_std`. The rest have no
pool to swap and exit on a flag they do not define, which is why
`script/servers.sh` passes these only to the nine.

`uwebsockets` is the odd one: it is a C++ server, so none of the Go pools can
run under it. It has one thread pool of its own, built the way the `uws` pool
is - workers up front over sharded queues, refusing rather than waiting - and
what it reads out of the flag is where a server answers from. `default` and
`inline` install no pool, which for uWS means the loop callback; every other
mode hands the callback to a goroutine off the event loop, and its thread pool
stands in for that. It logs which way it read the flag, and exits on a name
that names no mode, as the Go servers do. `-tpmax` defaults to one worker per
core rather than to 256, since these are OS threads doing non-blocking work,
and `-tpmin` is ignored. See [its README](frameworks/uwebsockets/README.md).

Every report carries a `Pool` column naming the pool its server installed,
which each client reads from that server's own `/taskpool` route when it
builds the report. It is what ran rather than what the run asked for, so a
server whose own scheduling is one of these pools shows that pool under
`BENCH_TASKPOOL=default` rather than `default` - `uws_events` reports `uws`
there. `-` is a framework with no pool hook at all, and `uwebsockets` adds
which side of its loop the echo ran on, e.g. `nbio(pool)` or `default(loop)`.

Three things to keep in mind when reading a report:

- Whichever pool is selected, one connection's messages are still handled and
  answered in the order they arrived. No pool promises that by itself - `go`
  runs a goroutine per task - so the order comes from never handing a pool
  more than one task per connection at a time: fib submits the connection
  itself, and nbio, fnet, uws and greatws each keep one per-connection queue
  and submit a drain only when none is in flight. See the `Ordering` section
  of [the package doc](taskpool/taskpool.go) for which mechanism each
  framework uses. `uwebsockets` keeps a queue and a drain flag the same way,
  and has one more step to order: uWS is single threaded per loop, so a worker
  cannot write and hands the echo back through `uWS::Loop::defer`, whose queue
  is FIFO.
- `uws` is the only pool that refuses work rather than waiting for room, which
  is how uws surfaces application backpressure. fib and uws close the
  connections behind the work their pool refused; nbio and fnet run it on the
  caller instead, because a dropped task there would stall a connection rather
  than lose one message.
- `fnet`'s pool goes on its `websocket.Upgrader`, not on its `fnet.Server`:
  the latter runs the HTTP request loop and holds a worker for as long as a
  connection stays unupgraded, so a bounded pool there would wedge on the
  handshake burst rather than measure the callbacks. `-tpmax` and `-tpqueue`
  are read per shard by the `fnet` pool, which picks its own shard count from
  `GOMAXPROCS`.

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

## env
```sh
            .-/+oossssoo+/-.               
        `:+ssssssssssssssssss+:`           -------------------------
      -+ssssssssssssssssssyyssss+-         OS: Ubuntu 20.04.6 LTS on Windows 10 x86_64
    .ossssssssssssssssssdMMMNysssso.       Kernel: 5.15.146.1-microsoft-standard-WSL2
   /ssssssssssshdmmNNmmyNMMMMhssssss/      Uptime: 1 hour, 41 mins
  +ssssssssshmydMMMMMMMNddddyssssssss+     Packages: 739 (dpkg), 4 (snap)
 /sssssssshNMMMyhhyyyyhmNMMMNhssssssss/    Shell: bash 5.0.17
.ssssssssdMMMNhsssssssssshNMMMdssssssss.   Terminal: Relay(527)
+sssshhhyNMMNyssssssssssssyNMMMysssssss+   CPU: AMD Ryzen 9 7945HX with Radeon Graphics (32) @ 2.495GHz
ossyNMMMNyMMhsssssssssssssshmmmhssssssso   GPU: f008:00:00.0 Microsoft Corporation Device 008e
ossyNMMMNyMMhsssssssssssssshmmmhssssssso   Memory: 645MiB / 31951MiB
+sssshhhyNMMNyssssssssssssyNMMMysssssss+
.ssssssssdMMMNhsssssssssshNMMMdssssssss.
 /sssssssshNMMMyhhyyyyhdNMMMNhssssssss/
  +sssssssssdmydMMMMMMMMddddyssssssss+
   /ssssssssssshdmNNNNmyNMMMMhssssss/
    .ossssssssssssssssssdMMMNysssso.
      -+sssssssssssssssssyyyssss+-
        `:+ssssssssssssssssss+:`
            .-/+oossssoo+/-.
```



## 10k connections, 1k payload, benchmark for all frameworks
run:
```sh
git clone https://github.com/lesismal/go-websocket-benchmark.git
cd go-websocket-benchmark
./script/benchmark.sh

# if you want to change the benchmark config, just read the script and edit:
# go-websocket-benchmark/script/config.sh
```

results:

----------------------------------------------------------------------------------------------------
20240403 16:31.05.050 [BenchEcho] Report

| Framework        | TPS    | EER     | Min     | Avg     | Max      | TP50    | TP75    | TP90    | TP95    | TP99     | Used  | Total   | Success | Failed | Conns | Concurrency | Payload | CPU Min | CPU Avg | CPU Max | MEM Min | MEM Avg | MEM Max |
| ---------------- | ------ | ------- | ------- | ------- | -------- | ------- | ------- | ------- | ------- | -------- | ----- | ------- | ------- | ------ | ----- | ----------- | ------- | ------- | ------- | ------- | ------- | ------- | ------- |
| fasthttp         | 770272 | 860.50  | 19.22us | 12.92ms | 156.10ms | 11.08ms | 12.77ms | 19.26ms | 20.36ms | 32.31ms  | 2.60s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 677.71  | 895.15  | 1136.53 | 335.48M | 335.48M | 335.48M |
| gobwas           | 586901 | 505.42  | 15.13us | 16.83ms | 219.14ms | 11.93ms | 18.49ms | 28.34ms | 48.72ms | 103.06ms | 3.41s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 583.30  | 1161.22 | 1451.58 | 400.59M | 415.20M | 429.80M |
| gorilla          | 763962 | 831.85  | 14.81us | 13.04ms | 136.80ms | 10.97ms | 13.09ms | 19.61ms | 21.21ms | 48.99ms  | 2.62s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 704.82  | 918.39  | 1131.96 | 288.24M | 288.24M | 288.24M |
| gws              | 759921 | 776.50  | 11.82us | 13.12ms | 156.34ms | 10.83ms | 14.53ms | 19.60ms | 21.46ms | 46.94ms  | 2.63s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 760.20  | 978.65  | 1203.86 | 216.65M | 216.65M | 216.65M |
| gws_std          | 790027 | 917.34  | 16.70us | 12.60ms | 135.46ms | 10.59ms | 12.29ms | 19.02ms | 20.17ms | 51.95ms  | 2.53s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 585.79  | 861.21  | 1136.64 | 217.44M | 217.44M | 217.44M |
| hertz            | 475530 | 764.06  | 1.07ms  | 20.95ms | 72.61ms  | 18.44ms | 22.35ms | 34.48ms | 36.80ms | 42.35ms  | 4.21s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 164.81  | 622.37  | 780.84  | 523.80M | 556.98M | 590.17M |
| hertz_std        | 724648 | 1041.27 | 17.86us | 13.74ms | 236.25ms | 11.40ms | 12.99ms | 19.87ms | 22.16ms | 93.24ms  | 2.76s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 0.00    | 695.93  | 1194.34 | 360.41M | 361.41M | 362.41M |
| nbio_blocking    | 763517 | 840.92  | 15.91us | 13.02ms | 125.83ms | 10.96ms | 13.41ms | 19.41ms | 20.61ms | 49.08ms  | 2.62s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 693.08  | 907.96  | 1122.83 | 213.23M | 213.23M | 213.23M |
| nbio_mixed       | 789053 | 917.15  | 14.92us | 12.60ms | 148.03ms | 10.54ms | 12.94ms | 19.24ms | 20.27ms | 55.67ms  | 2.53s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 603.90  | 860.33  | 1127.10 | 272.71M | 272.71M | 272.71M |
| nbio_nonblocking | 717496 | 1044.34 | 29.75us | 13.90ms | 168.11ms | 12.07ms | 13.85ms | 20.14ms | 21.29ms | 35.10ms  | 2.79s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 0.00    | 687.04  | 1142.56 | 86.80M  | 95.38M  | 103.96M |
| nbio_std         | 753412 | 821.57  | 11.66us | 13.23ms | 223.49ms | 10.92ms | 13.05ms | 19.54ms | 21.57ms | 77.76ms  | 2.65s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 719.26  | 917.04  | 1114.81 | 202.95M | 202.95M | 202.95M |
| nettyws          | 778445 | 892.59  | 14.18us | 12.79ms | 142.58ms | 10.86ms | 13.31ms | 19.36ms | 20.35ms | 27.50ms  | 2.57s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 636.37  | 872.12  | 1127.89 | 189.11M | 189.11M | 189.11M |
| nhooyr           | 659804 | 651.89  | 19.06us | 15.10ms | 181.33ms | 11.14ms | 14.08ms | 24.10ms | 37.20ms | 107.87ms | 3.03s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 24.98   | 1012.14 | 1515.70 | 386.97M | 386.97M | 386.97M |
| quickws          | 782685 | 944.61  | 15.31us | 12.71ms | 139.09ms | 10.78ms | 12.24ms | 19.28ms | 20.40ms | 41.09ms  | 2.56s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 578.30  | 828.58  | 1086.75 | 150.31M | 150.31M | 150.31M |
| greatws          | 648302 | 983.04  | 30.80us | 15.35ms | 302.55ms | 13.35ms | 16.50ms | 21.74ms | 24.18ms | 62.52ms  | 3.08s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 68.93   | 659.49  | 973.74  | 154.16M | 154.71M | 155.25M |
| greatws_event    | 657385 | 1158.59 | 28.88us | 15.17ms | 190.30ms | 13.45ms | 16.33ms | 21.33ms | 22.78ms | 31.36ms  | 3.04s | 2000000 | 2000000 | 0      | 10000 | 10000       | 1024    | 11.99   | 567.40  | 845.77  | 161.71M | 163.85M | 165.98M |
----------------------------------------------------------------------------------------------------
----------------------------------------------------------------------------------------------------
20240403 16:31.05.064 [BenchRate] Report

| Framework        | Duration | EchoEER | Packet Sent | Bytes Sent | Packet Recv | Bytes Recv | Conns | SendRate | Payload | CPU Min | CPU Avg | CPU Max | MEM Min | MEM Avg | MEM Max |
| ---------------- | -------- | ------- | ----------- | ---------- | ----------- | ---------- | ----- | -------- | ------- | ------- | ------- | ------- | ------- | ------- | ------- |
| fasthttp         | 10.00s   | 1867.33 | 19900000    | 18.98G     | 19868546    | 18.95G     | 10000 | 200      | 1024    | 1043.72 | 1064.01 | 1092.89 | 368.16M | 402.69M | 461.00M |
| gobwas           | 10.00s   | 650.82  | 11001720    | 10.49G     | 10675692    | 10.18G     | 10000 | 200      | 1024    | 1528.81 | 1640.33 | 1744.65 | 423.34M | 431.77M | 442.66M |
| gorilla          | 10.00s   | 2079.85 | 19900000    | 18.98G     | 19900000    | 18.98G     | 10000 | 200      | 1024    | 0.00    | 956.80  | 1078.91 | 328.34M | 397.06M | 464.05M |
| gws              | 10.00s   | 1866.56 | 19850870    | 18.93G     | 19850870    | 18.93G     | 10000 | 200      | 1024    | 0.00    | 1063.50 | 1207.03 | 245.38M | 251.12M | 259.20M |
| gws_std          | 10.00s   | 2069.83 | 19887960    | 18.97G     | 19887960    | 18.97G     | 10000 | 200      | 1024    | 0.00    | 960.85  | 1075.37 | 251.84M | 257.60M | 266.69M |
| hertz            | 10.00s   | 1471.93 | 15691030    | 14.96G     | 15521809    | 14.80G     | 10000 | 200      | 1024    | 1028.86 | 1054.52 | 1098.86 | 756.00M | 786.49M | 841.21M |
| hertz_std        | 10.00s   | 1752.33 | 19879500    | 18.96G     | 19821035    | 18.90G     | 10000 | 200      | 1024    | 1117.02 | 1131.13 | 1148.89 | 396.29M | 460.64M | 504.64M |
| nbio_blocking    | 10.00s   | 2004.79 | 19884530    | 18.96G     | 19884530    | 18.96G     | 10000 | 200      | 1024    | 0.00    | 991.85  | 1116.45 | 238.64M | 252.28M | 270.88M |
| nbio_mixed       | 10.00s   | 1854.41 | 19899920    | 18.98G     | 19881304    | 18.96G     | 10000 | 200      | 1024    | 1046.51 | 1072.11 | 1092.85 | 427.79M | 451.41M | 464.98M |
| nbio_nonblocking | 10.00s   | 1548.99 | 19649200    | 18.74G     | 19559255    | 18.65G     | 10000 | 200      | 1024    | 1217.77 | 1262.71 | 1285.73 | 136.00M | 158.42M | 181.10M |
| nbio_std         | 10.00s   | 1968.85 | 19849750    | 18.93G     | 19849750    | 18.93G     | 10000 | 200      | 1024    | 0.00    | 1008.19 | 1140.91 | 222.25M | 242.36M | 265.38M |
| nettyws          | 10.00s   | 1924.26 | 19900000    | 18.98G     | 19900000    | 18.98G     | 10000 | 200      | 1024    | 979.88  | 1034.16 | 1061.80 | 205.34M | 206.97M | 207.56M |
| nhooyr           | 10.00s   | 1150.49 | 18659680    | 17.80G     | 18534252    | 17.68G     | 10000 | 200      | 1024    | 1575.61 | 1610.99 | 1645.84 | 388.97M | 391.11M | 391.66M |
| quickws          | 10.00s   | 2238.76 | 19900000    | 18.98G     | 19900000    | 18.98G     | 10000 | 200      | 1024    | 0.00    | 888.89  | 1010.88 | 154.31M | 155.87M | 156.31M |
| greatws          | 10.00s   | 1626.12 | 19643320    | 18.73G     | 19581458    | 18.67G     | 10000 | 200      | 1024    | 1151.57 | 1204.18 | 1240.71 | 154.30M | 162.51M | 170.98M |
| greatws_event    | 10.00s   | 1914.01 | 19889190    | 18.97G     | 19858307    | 18.94G     | 10000 | 200      | 1024    | 984.68  | 1037.52 | 1058.69 | 154.58M | 163.28M | 177.87M |
----------------------------------------------------------------------------------------------------


## 1m connections, 1k payload, benchmark for nbio/greatws

run:
```sh
git clone https://github.com/lesismal/go-websocket-benchmark.git
cd go-websocket-benchmark
./script/1m_conns_benchmark.sh
```

result:

----------------------------------------------------------------------------------------------------
20240403 16:35.28.724 [Connections] Report

| Framework        | TPS   | Min  | Avg     | Max    | TP50 | TP75 | TP90 | TP95 | TP99  | Used   | Total   | Success | Failed | Concurrency |
| ---------------- | ----- | ---- | ------- | ------ | ---- | ---- | ---- | ---- | ----- | ------ | ------- | ------- | ------ | ----------- |
| nbio_nonblocking | 64058 | 10ns | 30.94ms | 15.61s | 20ns | 20ns | 21ns | 30ns | 31ns  | 15.61s | 1000000 | 1000000 | 0      | 2000        |
| greatws          | 63882 | 10ns | 31.04ms | 15.65s | 20ns | 21ns | 30ns | 40ns | 121ns | 15.65s | 1000000 | 1000000 | 0      | 2000        |
| greatws_event    | 69324 | 10ns | 28.61ms | 14.42s | 20ns | 20ns | 30ns | 31ns | 51ns  | 14.43s | 1000000 | 1000000 | 0      | 2000        |
----------------------------------------------------------------------------------------------------
20240403 16:35.28.732 [BenchEcho] Report

| Framework        | TPS    | EER    | Min     | Avg     | Max   | TP50    | TP75    | TP90     | TP95     | TP99     | Used   | Total   | Success | Failed | Conns   | Concurrency | Payload | CPU Min | CPU Avg | CPU Max | MEM Min | MEM Avg | MEM Max |
| ---------------- | ------ | ------ | ------- | ------- | ----- | ------- | ------- | -------- | -------- | -------- | ------ | ------- | ------- | ------ | ------- | ----------- | ------- | ------- | ------- | ------- | ------- | ------- | ------- |
| nbio_nonblocking | 152342 | 440.12 | 27.08us | 65.54ms | 1.08s | 34.59ms | 37.14ms | 133.20ms | 367.50ms | 453.01ms | 13.13s | 2000000 | 2000000 | 0      | 1000000 | 10000       | 1024    | 189.15  | 346.13  | 496.95  | 967.02M | 967.58M | 968.51M |
| greatws          | 141385 | 412.66 | 25.55us | 70.62ms | 1.05s | 37.50ms | 42.03ms | 143.13ms | 373.50ms | 463.37ms | 14.15s | 2000000 | 2000000 | 0      | 1000000 | 10000       | 1024    | 112.40  | 342.62  | 399.85  | 575.97M | 576.48M | 576.86M |
| greatws_event    | 145457 | 514.79 | 24.77us | 68.66ms | 1.00s | 35.80ms | 38.67ms | 140.22ms | 373.21ms | 453.04ms | 13.75s | 2000000 | 2000000 | 0      | 1000000 | 10000       | 1024    | 48.71   | 282.56  | 340.90  | 447.33M | 448.25M | 448.86M |
----------------------------------------------------------------------------------------------------
