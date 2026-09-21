# uwebsockets

Echo WebSocket server built on [uWebSockets](https://github.com/uNetworking/uWebSockets)
v20.74.0 (the same pinned version and uSockets submodule used by `benchcli-uwscpp`), using
the high-level `uWS::App` server API instead of the low-level protocol headers the C++
benchmark client uses. One `uWS::App`/event loop runs per hardware thread; every loop
listens on all of the framework's benchmark ports, relying on uSockets' default
`SO_REUSEPORT` behavior to spread accepted connections across threads.

`/init` and `/ps` replicate just enough of `frameworks.HandleCommon` (see
`frameworks/handlers.go`) for the benchmark clients' CPU/RSS reporting, sampling
`/proc/self/stat` and `/proc/self/status` directly instead of linking `gopsutil`. Both
routes are registered on every benchmark port rather than a separate pid port, so
`config.Ports["uwebsockets"]`'s last port doubles as the control port (no entry needed in
`config.InitAndGetFrameworkPid`'s pid-port-offset list).

## Caveats

- uSockets always enables `TCP_NODELAY` on accepted sockets and does not expose a way to
  disable it, so `-nodelay=false` is accepted (for CLI compatibility with the other
  frameworks) but has no effect; the server logs a note when it's passed.
- `/debug/pprof/*` isn't implemented (there's no Go runtime to profile), so
  `-ep=true`/`-rp=true` pprof capture is skipped for this framework.

## Build

```sh
# Debian/Ubuntu prerequisites
sudo apt-get install build-essential git zlib1g-dev

# Build just this server (first build downloads pinned uWebSockets/uSockets).
bash frameworks/uwebsockets/build.sh

# Built automatically as part of the full benchmark.
bash script/benchmark.sh
```

The build caches uWebSockets/uSockets sources in `.deps/` and objects in `.build/`
(both ignored by Git and independent from `benchcli-uwscpp`'s own `.deps`/`.build`).
