package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/taskpool"

	fib "github.com/lesismal/fib/go"
	"github.com/lesismal/fib/go/websocket"
)

var (
	nodelay  = flag.Bool("nodelay", true, `tcp nodelay`)
	payload  = flag.Int("b", 1024, `read buffer size`)
	_        = flag.Int("mrb", 4096, `max read buffer size`)
	memLimit = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_        = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_        = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)
)

const (
	// connectionPendingHighWatermark bounds queued output on one connection.
	// Once crossed, epoll pauses reads for that connection until the queue falls
	// to one quarter of this value. A small per-connection bound prevents a slow
	// peer from monopolising the process-wide budget.
	connectionPendingHighWatermark = 64 << 10

	// processPendingBudget bounds queued output across the single server and all
	// benchmark connections. Without this aggregate bound, the per-client
	// watermark alone could permit roughly 1.6GB of pending payload.
	processPendingBudget = int64(1024) << 20
)

func main() {
	flag.Parse()

	addrs, err := config.GetFrameworkServerAddrs(config.Fib)
	if err != nil {
		logging.Fatalf("GetFrameworkServerAddrs(%v) failed: %v", config.Fib, err)
	}
	server := startServer(addrs)
	metricsServer := startMetricsServer()
	// Sample the backpressure counters through the run as well as at the end:
	// one total cannot say which phase the pauses belong to.
	go func() {
		for range time.Tick(500 * time.Millisecond) {
			logStatus(server.Stats())
		}
	}()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt

	logStatus(server.Stats())
	server.Stop()
	if err := metricsServer.Shutdown(context.Background()); err != nil {
		logging.Printf("metrics server shutdown failed: %v", err)
	}
}

func logStatus(status fib.Stats) {
	logging.Printf(
		"Status: ReadsPausedByWatermark=%d ReadsPausedByBudget=%d ReadsResumed=%d PendingBytes=%d",
		status.ReadsPausedByWatermark,
		status.ReadsPausedByBudget,
		status.ReadsResumed,
		status.PendingBytes,
	)
}

// startServer binds every benchmark port to one server. The alternative, a
// server per port, gives each one its own event loop but also its own
// descriptor table, command queue, buffer pools and outbound budget, none of
// which the ports have any reason not to share: with 50 of them the descriptor
// tables alone held 50MB, since each table is indexed by descriptor and so
// sized by the highest one the process had reached rather than by the
// connections that server actually held.
func startServer(addrs []string) *fib.Engine {
	websocketHandler := websocket.NewHandler(websocket.HandlerFuncs{
		Message: func(c *websocket.Connection, opcode websocket.Opcode, data []byte) {
			if err := c.WriteMessage(opcode, data); err != nil {
				_ = c.Close(websocket.CloseInternalError, "write failed")
			}
		},
	})
	handler := &serverHandler{Handler: websocketHandler, nodelay: *nodelay}

	serverConfig := fib.DefaultConfig()
	serverConfig.Network = "tcp4"
	serverConfig.Addrs = addrs

	// fib's own pool is ModeAdaptive, which is also what -taskpool
	// defaults to, so the default run measures the same arrangement
	// reached through the shared registry. Another -taskpool runs the
	// engine on another framework's scheduler; -taskpool=default leaves
	// fib to build its pool itself.
	if pool := taskpool.FromFlags(); pool != nil {
		serverConfig.SetTaskPool(taskpool.FibTaskPool{Pool: pool})
	}

	server, err := fib.Bind(serverConfig, handler)
	if err != nil {
		logging.Fatalf("bind %d addresses failed: %v", len(addrs), err)
	}
	go func() {
		if err := server.Run(); err != nil {
			logging.Printf("server exit: %v", err)
		}
		if err := server.Close(); err != nil {
			logging.Printf("server close failed: %v", err)
		}
	}()
	return server
}

type serverHandler struct {
	fib.Handler
	nodelay bool
}

func (h *serverHandler) OnOpen(c *fib.Connection) {
	if h.nodelay {
		if err := syscall.SetsockoptInt(c.FD(), syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1); err != nil {
			c.Close()
			return
		}
	}
	h.Handler.OnOpen(c)
}

func startMetricsServer() *http.Server {
	addr, err := config.GetFrameworkHTTPServerAddrs(config.Fib)
	if err != nil {
		logging.Fatalf("GetFrameworkHTTPServerAddrs(%v) failed: %v", config.Fib, err)
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
