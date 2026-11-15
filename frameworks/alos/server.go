//go:build linux && amd64

package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"

	"github.com/guno1928/alos-http/core"
)

var (
	nodelay = flag.Bool("nodelay", true, `tcp nodelay`)
	payload = flag.Int("b", 1024, `read buffer size`)
	_       = flag.Int("mrb", 4096, `max read buffer size`)
	_       = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_       = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_       = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)
)

// wsOptions sizes the largest message alos accepts: its 4MiB default, or the
// payload if the run asks for more.
var wsOptions core.WSUpgradeOptions

func main() {
	flag.Parse()

	if !*nodelay {
		// alos sets TCP_NODELAY on every connection it accepts, and an
		// event-driven WebSocket never hands its socket to the handler.
		logging.Printf("%v server: -nodelay=false is ignored, alos always sets TCP_NODELAY", config.Alos)
	}
	wsOptions.MaxMessageSize = max(4<<20, int64(*payload))

	addrs, err := config.GetFrameworkServerAddrs(config.Alos)
	if err != nil {
		logging.Fatalf("GetFrameworkServerAddrs(%v) failed: %v", config.Alos, err)
	}
	servers, done := startServers(addrs)
	metricsServer := startMetricsServer()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt

	// Bound the shutdown: alos's Shutdown drains connections and may not
	// return, and a server that outlives its turn holds up the next one.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan struct{})
	go func() {
		for _, s := range servers {
			_ = s.Shutdown(ctx)
		}
		done.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-ctx.Done():
		logging.Printf("%v server: shutdown did not finish in time, exiting anyway", config.Alos)
	}
	if err := metricsServer.Shutdown(context.Background()); err != nil {
		logging.Printf("metrics server shutdown failed: %v", err)
	}
}

// startServers runs one alos server per port, since core.Config takes one
// address. Each gets its share of the CPUs as SO_REUSEPORT listeners, each
// listener an epoll loop: max(1, ceil(GOMAXPROCS/ports)). On one port
// (BENCH_PORTS=1) that is alos's own default of a loop per CPU; on fifty it
// is one each, since alos's default on every one of them would be fifty times
// as many loops as CPUs. alos has no way to share loops between servers, so
// -eventloops cannot set their number, and alos is not among the
// eventloop_frameworks.
func startServers(addrs []string) ([]*core.Server, *sync.WaitGroup) {
	procs := runtime.GOMAXPROCS(0)
	listeners := max(1, (procs+len(addrs)-1)/len(addrs))
	servers := make([]*core.Server, 0, len(addrs))
	for _, addr := range addrs {
		s := core.New(core.Config{
			Addr:        addr,
			PlainHTTP:   true,
			Listeners:   listeners,
			WorkerCount: procs,
			// HTTP/1 upgrades only: the clients never speak cleartext HTTP/2.
			DisableHTTP2: true,
			// Off the limits a public server wants and a benchmark client
			// holding a million connections from one address trips: alos
			// defaults to 8192 connections in all, 20 per client address,
			// HTTP timeouts, and WebSocket read and write timeouts of 120s
			// and 30s that would close the idle connections of a long run.
			MaxConns:            -1,
			MaxConnsPerIP:       -1,
			ReadTimeout:         -1,
			ReadHeaderTimeout:   -1,
			WriteTimeout:        -1,
			IdleTimeout:         -1,
			WSReadTimeout:       -1,
			WSWriteTimeout:      -1,
			MaxReadSize:         max(2<<20, 2*int64(*payload)),
			WebSocketOriginMode: core.WSOriginOff,
		})
		s.Router.GET("/ws", onUpgrade)
		servers = append(servers, s)
	}

	done := &sync.WaitGroup{}
	for i, s := range servers {
		done.Add(1)
		go func() {
			defer done.Done()
			if err := s.ListenAndServe(); err != nil {
				logging.Fatalf("listen %v failed: %v", addrs[i], err)
			}
		}()
	}
	logging.Printf("%v server: listening on %d ports, %v to %v, %d listeners (event loops) each, GOMAXPROCS %d",
		config.Alos, len(addrs), addrs[0], addrs[len(addrs)-1], listeners, procs)
	return servers, done
}

// handler echoes every message back with its opcode. ServeWebSocketEvents
// keeps the connection on its epoll loop and calls OnMessage there, rather
// than parking a goroutine per connection in ReadMessage as ServeWebSocket
// does; WriteMessage copies msg into the connection's outbox, so msg can be
// reused once it returns.
var handler = core.WSEventHandler{
	OnMessage: func(ws *core.WSConn, opcode byte, msg []byte) {
		if err := ws.WriteMessage(opcode, msg); err != nil {
			_ = ws.Close()
		}
	},
}

func onUpgrade(req *core.Request, resp *core.Response) {
	core.ServeWebSocketEvents(req, resp, wsOptions, handler)
}

// startMetricsServer serves /init, /ps and pprof on the port after the
// benchmark ones, on net/http, as fib does, so that they stay off alos's loops.
func startMetricsServer() *http.Server {
	addr, err := config.GetFrameworkHTTPServerAddrs(config.Alos)
	if err != nil {
		logging.Fatalf("GetFrameworkHTTPServerAddrs(%v) failed: %v", config.Alos, err)
	}
	mux := http.NewServeMux()
	frameworks.HandleCommon(mux)
	server := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.Printf("metrics server exit: %v", err)
		}
	}()
	return server
}
