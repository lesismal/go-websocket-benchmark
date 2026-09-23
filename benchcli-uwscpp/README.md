# benchcli-uwscpp

Standalone C++17 benchmark client using [uWebSockets](https://github.com/uNetworking/uWebSockets)
v20.74.0 (`c445faa38125bf782eed3fec97f83b4733c7fb91`) and its pinned uSockets submodule.
It uses `uWS::WebSocketProtocol<false>`, `uWS::protocol::formatMessage<false>` and
`uWS::WebSocketHandshake`, with one uSockets event loop per native worker thread.
Modern uWebSockets exposes server-oriented App APIs, so the client HTTP upgrade,
connection scheduling and benchmark orchestration are implemented here. WebSocket
parsing and frame encoding use the upstream C++ implementation.

## Build and selection

Run commands from the repository root. Linux and macOS are supported. Install a
C/C++17 compiler, Git, Python 3 and libcurl development headers/library; the full
benchmark also needs the Go version specified in `go.mod`.

```sh
# Debian/Ubuntu C++ prerequisites
sudo apt-get install build-essential git python3 libcurl4-openssl-dev

# Build just the C++ client (first build downloads pinned dependencies).
bash benchcli-uwscpp/build.sh

# The full benchmark defaults to C++ via script/config.sh.
bash script/benchmark.sh

# Select the original Go client instead.
BENCH_CLIENT=benchcli-go bash script/benchmark.sh
```

Edit `BENCH_CLIENT=${BENCH_CLIENT:-benchcli-uwscpp}` in `script/config.sh` to change the
persistent default. Both builds produce `output/bin/bench.client`; rerun the build
when switching clients. The runner and report scripts use that binary.

The build caches sources in `.deps/` and objects in `.build/` (both ignored by Git,
and preserved when `output/` is cleaned). It downloads nlohmann/json v3.12.0 for
JSON parsing; libcurl is used only for server control, metrics and pprof HTTP calls.
No Go binary, Python process or libcurl WebSocket API participates in the C++
benchmark at runtime. TLS is not enabled in uSockets: the benchmark endpoints in
`config/config.go` are all `ws://`.

For offline builds, provide existing dependency source directories:

```sh
UWEBSOCKETS_DIR=/path/to/uWebSockets \
NLOHMANN_JSON_DIR=/path/to/json \
bash benchcli-uwscpp/build.sh /absolute/path/to/bench.client
```

The uWebSockets tree must include its uSockets submodule. `CC`, `CXX`, `CFLAGS`,
`CXXFLAGS`, `LDFLAGS` and `BUILD_DIR` can also be overridden. On macOS, install the
Xcode command line tools and Python 3; the macOS SDK includes libcurl.

## Functionality and flags

Existing Go flags and defaults are accepted (`-flag=value`, `-flag value`, and
`-bool`/`-bool=false`). `-h` lists all defaults.

| Flags | Function |
| --- | --- |
| `-f`, `-ip`, `-nodelay` | Framework, server IP/hostname and TCP_NODELAY |
| `-c`, `-dc`, `-dt`, `-dr`, `-dri` | Connections, concurrent dials, TCP+upgrade timeout, attempts, retry interval |
| `-b`, `-check`, `-tpn`, `-pi` | Payload size, binary response validation, latency percentiles, server sampling interval in milliseconds |
| `-ps` | Where the server's CPU and MEM samples come from: `auto` (default) samples the server process here when it runs on this machine and asks it over `/ps` when it does not, `local` always samples here, `remote` always asks |
| `-ec`, `-en`, `-el` | Echo concurrency, measured round trips, global messages/second limit |
| `-ep`, `-epd` | Echo CPU/heap profiles and CPU profile duration in seconds |
| `-rate`, `-rc`, `-rd`, `-rr`, `-rbs`, `-rl` | Enable Rate, sending groups, seconds, messages/connection/second, batch byte budget, global messages/second limit |
| `-rp`, `-rpd` | Rate CPU/heap profiles and CPU profile duration in seconds |
| `-r`, `-preffix`, `-suffix` | Aggregate reports, filename prefix (original spelling), filename suffix |
| `-sort` | Report row order: `result` (default) ranks the best result first - TPS for Connections and BenchEcho, Packet Recv then EER for BenchRate, with each row's percentage of the best shown in that column - and `framework` keeps the `config.FrameworkList` order. Ties keep the framework order in both |
| `-m` | Native memory budget in bytes; 0 disables it |
| `-threads` | C++ event-loop thread count; 0 uses available CPUs, capped by concurrency |
| `-io-timeout` | C++ echo response timeout, default 30s |

The client retains established connections between stages. Echo warms up with
`min(successful connections * 5, 2000000)` round trips, then measures exactly
`-en` attempts. Workers rotate through their connections, with at most `-ec`
round trips outstanding in total. Rate divides all live connections across
`-rc` sending groups. Batches fit `-rbs` and divide `-rr`; if a frame is larger
than the byte budget, one frame is sent. Per-connection outstanding batches and
partial writes are bounded. Latencies and report durations are nanoseconds;
SendBytes/RecvBytes count payload bytes, excluding WebSocket framing.

Framework order, port ranges and report schemas are generated at build time from
`config/config.go` and `benchcli-go/report/*.go`, so adding a framework or changing
a report field updates both clients on the next build. JSON files retain the Go
names and fields and can be aggregated by either client. Markdown uses the same
columns, units and framework order, and the same Summary table of the run's
parameters (`summary:"<name>"` fields) in front of the three.

```sh
./output/bin/bench.client -f=gorilla -c=10000 -ec=10000 -en=2000000 \
  -check=true -rate=true -rd=10 -rr=200 -preffix=cpp_
./output/bin/bench.client -r=true -preffix=cpp_
```

## Intentional native-client differences

- `-m` is a Linux address-space ceiling (`RLIMIT_AS`). macOS does not implement
  that limit, so peak RSS is checked every 100ms during benchmark stages instead;
  transient allocation can exceed that budget. This is not Go's soft GC target.
- `-dc`, `-ec` and `-rc` describe logical concurrency, not a native thread per
  connection. Linux CPU affinity from `taskset` is respected when sizing workers.
- `-el` and `-rl` implement their documented **messages per second** semantics,
  using a shared token bucket with an initial one-second burst. They do not copy
  the Go client's current `rate.Every(time.Second)` one-token/second bug.
- Connection latency is measured per logical dial, including its retries;
  successful Echo latency covers sending through receipt/validation. Percentiles
  use nearest-rank successful samples. Failed operations are excluded.
- Echo responses time out rather than hanging forever. Zero established
  connections, failed connections/echoes and invalid arguments produce nonzero
  exit codes. Missing resource/pprof endpoints are reported as warnings, and
  unavailable resource statistics remain zero. Zero CPU avoids invalid JSON NaN/Inf.
- Profile jobs start two seconds after warmup/Rate starts. The client waits for
  them before proceeding to the next benchmark or exiting, so short runs can take
  longer when profiling is enabled. Use `-ep=false -rp=false` for short smoke tests.
- Like the Go client, Rate reports the receive count at the end of its duration;
  late responses are not included and there is no drain phase.

## Tests

The Python fixture binds the Gorilla test port range (127.0.0.1:12001–12050).
Run with no other Gorilla benchmark server listening on those ports:

```sh
python3 benchcli-uwscpp/test_client.py ./output/bin/bench.client
python3 benchcli-uwscpp/test_scripts.py
```

It covers upgrades, retries/timeouts, masks, extended payload lengths, fragmented
messages with Ping, corruption/disconnect handling, throttling, batching, server
statistics, profile files, report aggregation and CLI errors. To check memory
safety, build with `CFLAGS` and `CXXFLAGS` containing
`-fsanitize=address,undefined -g`, set `LDFLAGS=-fsanitize=address,undefined`, and run
the same tests. Sanitizers require `-m=0` (already supplied by the fixture).
