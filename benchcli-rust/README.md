# benchcli-rust

The benchmark client in Rust, on [tokio-tungstenite](https://github.com/snapview/tokio-tungstenite)
0.30.0, and the one `script/config.sh` runs by default. It takes `benchcli-go`'s flags, loads a
server the way `benchcli-uwscpp` does, and writes the same JSON reports and markdown tables as
both, so a run from any of the three reads and diffs against a run from the others.

## How it loads a server

A fixed set of worker threads - one per CPU the process may run on (the affinity mask
`script/env.sh` pins the client to), capped by the concurrency flags, or `-threads` - each runs
a single-threaded Tokio runtime over its share of the connections, and nothing else touches
them. It is the arrangement `benchcli-uwscpp` has with one uSockets loop per thread. Every
connection is a task on its worker's runtime holding a tokio-tungstenite `WebSocketStream`;
which of them sends what, and when, is decided in one place per worker that hands each task
what to send and hears back what arrived. Every stage follows `benchcli-uwscpp`'s `engine.hpp`,
so that the two native clients put the same load on a server:

- **Connections**: at most `-dc` connections dialing at once, each attempt given `-dt` for its
  TCP connect and upgrade together, `-dr` attempts `-dri` apart, spread over the framework's
  ports.
- **BenchEcho**: `min(connections * 5, 2000000)` round trips of warmup, then exactly `-en`, with
  at most `-ec` outstanding at once, rotating through the idle connections. A response that takes
  longer than `-io-timeout` closes its connection and counts as failed.
- **BenchRate**: the live connections divided into `-rc` groups, each sent a batch of `Pipeline`
  frames every `Pipeline/-rr` seconds - `-rpl` frames, or as many as `-rbs` bytes hold, written
  as one - with a connection skipped while its last batch is still being written or five batches
  are unanswered.

`-el` and `-rl` are one token bucket shared by every worker, in messages per second.

Reading the server is tungstenite's: the parsing, the reassembly of fragmented messages and the
answers to pings, and the upgrade's key and accept value (`generate_key`, `derive_accept_key`).
The connection is handed to it with `WebSocketStream::from_partially_read` once the upgrade is
answered.

What the client sends is not framed per message. As `benchcli-uwscpp` does, every payload is
encoded once at startup as a masked binary frame, and a Rate batch is those frames back to back;
an echo or a batch is one write of bytes that never change. The socket under the
`WebSocketStream` (`src/stream.rs`) does two more things `benchcli-uwscpp` gets from uSockets and
uWS:

- It reads through one 512KiB buffer per worker thread, so one `recv` takes all a socket has,
  and hands tungstenite its 4KiB at a time from memory. Handed the socket directly, tungstenite
  reads 4KiB a call.
- A write the socket cannot take whole drains in the background while the connection goes on
  reading, the way uWS keeps the unsent tail of a write. Anything tungstenite writes itself, a
  pong or a close, goes out after those frames, never inside one.

Before these, framing every message through tungstenite and awaiting each write whole kept the
client's four cores saturated while `benchcli-uwscpp` used three. A connection whose socket was
full was not read until it drained, so the server's echoes piled up in the server. Against fib
at 50000 connections, 3 server CPUs and 4 client CPUs in Docker, averaged over three runs:

| | BenchEcho TPS | BenchRate TPS | fib MEM Max in BenchRate | client CPU | client RSS |
| --- | --- | --- | --- | --- | --- |
| before | 426k | 2.15M | 1.06G | 396% | 1.75GB |
| now | 440k | 3.15M | 93M | 353% | 1.02GB |
| `benchcli-uwscpp` | 446k | 3.07M | 134M | 313% | 566MB |

The old client's memory also lost a run against `tokio_tungstenite`, whose server holds about
8.8G at 50000 connections: in a 12.48GB container the kernel killed the client or the server in
BenchRate. The client now finishes it at about 800MB. The one thing done here (`src/upgrade.rs`) is checking that answer:
tungstenite's client handshake takes the `Connection` header for a single value and fails a
server that answers `Connection: keep-alive, Upgrade`, which RFC 6455 allows, and a benchmark
client that counts a conforming server's connections as failed is measuring itself. The check is
`benchcli-uwscpp`'s.

tungstenite's configuration holds the server to what `benchcli-uwscpp`'s uWS parser does: a
message or frame longer than the payload being echoed (or 125 bytes, whichever is more) closes
the connection. Its read buffer is 4KiB, tungstenite's own suggestion where there are many
connections, rather than its 128KiB default: the buffer is reserved for every connection, and
the 1M-connection script runs this client. That is still 4GiB at a million connections before
anything else a connection holds, where `benchcli-uwscpp` keeps nothing per connection but a
partial frame - and 4GiB is `-m`'s default, which on Linux is this client's address-space
ceiling. So `script/1m_conns_benchmark.sh` needs `-m=0` (or a larger `-m`) and the memory to
back it, or `BENCH_CLIENT=benchcli-uwscpp`.

## Build

```sh
# Needs cargo (Rust 1.85+; built and tested with 1.98) and python3.
bash benchcli-rust/build.sh

# Built automatically as part of the full benchmark, which runs it by default.
bash script/benchmark.sh

# One of the other two instead.
BENCH_CLIENT=benchcli-uwscpp bash script/benchmark.sh
BENCH_CLIENT=benchcli-go bash script/benchmark.sh
```

All three build `output/bin/bench.client`, which the runner and report scripts run.

`build.rs` runs `benchcli-uwscpp/generate_metadata.py` - the script `benchcli-uwscpp` builds its
header with - to generate the framework list, port ranges, languages and report schemas from
`config/config.go` and `benchcli-go/report/*.go`, so a framework or a report field added on the Go
side reaches this client on its next build. The crates are pinned by `Cargo.lock`, and `--locked`
fails a build rather than move off them; `script/Dockerfile.benchmark` fetches them while
building the image and builds with `CARGO_NET_OFFLINE=true`, as for `frameworks/tokio_tungstenite`.
Objects go to `target/`, which Git and the Docker build context ignore.

## Flags and reports

The flags are `benchcli-go`'s, with its defaults, as `-flag=value`, `-flag value` or a bare
`-bool`; `-h` lists them. The two native-client additions are `benchcli-uwscpp`'s as well:
`-threads` (event-loop threads; 0 is the available CPUs, capped by the concurrency flags) and
`-io-timeout` (the longest an echo response is waited for, 30s by default). Everything
`benchcli-uwscpp`'s README says under its flags and its native-client differences holds here -
`-ps`, `-sort`, `-project`, `-m` (an address-space ceiling on Linux, sampled peak RSS on macOS), profile
timing, the exit codes - and the reports' `Client` reads `rust`.

## Tests

```sh
cargo test --manifest-path benchcli-rust/Cargo.toml
bash benchcli-rust/build.sh
python3 benchcli-uwscpp/test_client.py benchcli-rust/target/release/benchcli-rust
```

The unit tests cover the upgrade answer, the duration flags, the HTTP responses the control
requests read, and the tables against what `benchcli-go` writes for the same cells.
`benchcli-uwscpp/test_client.py` is the end-to-end suite both native clients run: it takes the
binary to test, stands up a WebSocket fixture on the gorilla ports (127.0.0.1:12001-12050, so no
gorilla server may be running) and covers upgrades, retries and timeouts, masking, payload
lengths, fragmented messages with pings, corruption and disconnects, throttling, batching, server
statistics, profile files, report aggregation and argument errors.
