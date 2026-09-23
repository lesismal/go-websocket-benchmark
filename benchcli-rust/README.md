# benchcli-rust

The benchmark client in Rust, and the one `script/config.sh` runs by default. It takes
`benchcli-go`'s flags, loads a server the way `benchcli-uwscpp` does, and writes the same JSON
reports and markdown tables as both, so a run from any of the three reads and diffs against a
run from the others.

## How it loads a server

A fixed set of worker threads - one per CPU the process may run on (the affinity mask
`script/env.sh` pins the client to), capped by the concurrency flags, or `-threads` - each runs
its own [mio](https://github.com/tokio-rs/mio) event loop over its share of the connections, and
nothing else touches them. It is the arrangement `benchcli-uwscpp` has with one uSockets loop per
thread, and every stage follows that client's `engine.hpp`, so that the two native clients put
the same load on a server:

- **Connections**: at most `-dc` connections dialing at once, each attempt given `-dt` for its
  TCP connect and upgrade together, `-dr` attempts `-dri` apart, spread over the framework's
  ports.
- **BenchEcho**: `min(connections * 5, 2000000)` round trips of warmup, then exactly `-en`, with
  at most `-ec` outstanding at once, rotating through the idle connections. A response that takes
  longer than `-io-timeout` closes its connection and counts as failed.
- **BenchRate**: the live connections divided into `-rc` groups, each sent a batch of `Pipeline`
  frames every `Pipeline/-rr` seconds - `-rpl` frames, or as many as `-rbs` bytes hold - with a
  connection skipped while a write to it is pending or five batches are unanswered.

`-el` and `-rl` are one token bucket shared by every worker, in messages per second.

The WebSocket client side - the upgrade, masked frames out, a parser for the server's frames in -
is implemented here (`src/protocol.rs`), as `benchcli-go` implements its own, rather than taken
from a server library: at a million connections the difference between a parser that keeps
nothing per connection but a partial frame, reading from the event loop's shared buffer, and one
that gives every connection a read buffer, is the difference between fitting and not. It holds
the server to what `benchcli-uwscpp`'s uWS parser does: a frame longer than the payload being
echoed (or 125 bytes, whichever is more) closes the connection, as do a masked frame, a reserved
bit, an oversized or fragmented control frame and a stray continuation; fragmented messages are
reassembled, with pings answered between their fragments.

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
building the image and builds with `CARGO_NET_OFFLINE=true`, as for `frameworks/sockudo_ws`.
Objects go to `target/`, which Git and the Docker build context ignore.

## Flags and reports

The flags are `benchcli-go`'s, with its defaults, as `-flag=value`, `-flag value` or a bare
`-bool`; `-h` lists them. The two native-client additions are `benchcli-uwscpp`'s as well:
`-threads` (event-loop threads; 0 is the available CPUs, capped by the concurrency flags) and
`-io-timeout` (the longest an echo response is waited for, 30s by default). Everything
`benchcli-uwscpp`'s README says under its flags and its native-client differences holds here -
`-ps`, `-sort`, `-m` (an address-space ceiling on Linux, sampled peak RSS on macOS), profile
timing, the exit codes - and the reports' `Client` reads `rust`.

## Tests

```sh
cargo test --manifest-path benchcli-rust/Cargo.toml
bash benchcli-rust/build.sh
python3 benchcli-uwscpp/test_client.py benchcli-rust/target/release/benchcli-rust
```

The unit tests cover the frame parser (every length form split byte by byte, fragments around a
ping, each violation), the upgrade answer, the duration flags, the HTTP responses the control
requests read, and the tables against what `benchcli-go` writes for the same cells.
`benchcli-uwscpp/test_client.py` is the end-to-end suite both native clients run: it takes the
binary to test, stands up a WebSocket fixture on the gorilla ports (127.0.0.1:12001-12050, so no
gorilla server may be running) and covers upgrades, retries and timeouts, masking, payload
lengths, fragmented messages with pings, corruption and disconnects, throttling, batching, server
statistics, profile files, report aggregation and argument errors.
