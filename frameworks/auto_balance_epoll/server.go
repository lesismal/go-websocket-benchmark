//go:build linux

package main

import (
	"context"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"

	epoll "github.com/lesismal/auto-balance-epoll/go"
	"github.com/lesismal/auto-balance-epoll/go/http/websocket"
)

var (
	nodelay = flag.Bool("nodelay", true, `tcp nodelay`)
	payload = flag.Int("b", 1024, `read buffer size`)
	_       = flag.Int("mrb", 4096, `max read buffer size`)
	_       = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_       = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_       = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)
)

func main() {
	flag.Parse()

	addrs, err := config.GetFrameworkServerAddrs(config.AutoBalanceEpoll)
	if err != nil {
		logging.Fatalf("GetFrameworkServerAddrs(%v) failed: %v", config.AutoBalanceEpoll, err)
	}
	servers := startServers(addrs)
	metricsServer := startMetricsServer()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt

	for _, server := range servers {
		server.Stop()
	}
	if err := metricsServer.Shutdown(context.Background()); err != nil {
		logging.Printf("metrics server shutdown failed: %v", err)
	}
}

func startServers(addrs []string) []*epoll.Server {
	websocketHandler := websocket.NewHandler(websocket.HandlerFuncs{
		Message: func(c *websocket.Connection, opcode websocket.Opcode, data []byte) {
			if err := c.WriteMessage(opcode, data); err != nil {
				_ = c.Close(websocket.CloseInternalError, "write failed")
			}
		},
	})
	handler := &serverHandler{Handler: websocketHandler, nodelay: *nodelay}
	servers := make([]*epoll.Server, 0, len(addrs))
	for _, addr := range addrs {
		_, portText, err := net.SplitHostPort(addr)
		if err != nil {
			logging.Fatalf("parse server address %q failed: %v", addr, err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			logging.Fatalf("parse server port %q failed: %v", portText, err)
		}
		serverConfig := epoll.DefaultConfig()
		serverConfig.Port = uint16(port)
		serverConfig.ReadBufferSize = *payload + 1024
		server, err := epoll.Bind(serverConfig, handler)
		if err != nil {
			logging.Fatalf("bind %q failed: %v", addr, err)
		}
		servers = append(servers, server)
		go func() {
			if err := server.Run(); err != nil {
				logging.Printf("server exit: %v", err)
			}
			if err := server.Close(); err != nil {
				logging.Printf("server close failed: %v", err)
			}
		}()
	}
	return servers
}

type serverHandler struct {
	epoll.Handler
	nodelay bool
}

func (h *serverHandler) OnOpen(c *epoll.Connection) {
	if h.nodelay {
		if err := syscall.SetsockoptInt(c.FD(), syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1); err != nil {
			c.Close()
			return
		}
	}
	h.Handler.OnOpen(c)
}

func startMetricsServer() *http.Server {
	addr, err := config.GetFrameworkHTTPServerAddrs(config.AutoBalanceEpoll)
	if err != nil {
		logging.Fatalf("GetFrameworkHTTPServerAddrs(%v) failed: %v", config.AutoBalanceEpoll, err)
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
