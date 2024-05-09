package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"

	"github.com/lxzan/gws"
)

var (
	nodelay = flag.Bool("nodelay", true, `tcp nodelay`)
	_       = flag.Int("b", 1024, `read buffer size`)
	_       = flag.Int("mrb", 4096, `max read buffer size`)
	_       = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_       = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_       = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)
)

func main() {
	flag.Parse()

	addrs, err := config.GetFrameworkServerAddrs(config.Gws)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Gws, err)
	}
	lns := startServers(addrs)
	pidServerAddr, err := config.GetFrameworkHTTPServerAddrs(config.Gws)
	if err != nil {
		logging.Fatalf("GetFrameworkHTTPServerAddrs(%v) failed: %v", config.Gws, err)
	}
	var pidLn net.Listener
	go func() {
		mux := &http.ServeMux{}
		frameworks.HandleCommon(mux)
		ln, err := frameworks.Listen("tcp", pidServerAddr)
		if err != nil {
			logging.Fatalf("Listen failed: %v", err)
		}
		pidLn = ln
		log.Printf("pid server exit: %v", http.Serve(ln, mux))
	}()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	for _, ln := range lns {
		ln.Close()
	}
	pidLn.Close()
}

func startServers(addrs []string) []net.Listener {
	lns := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		server := gws.NewServer(new(Handler), &gws.ServerOption{})
		ln, err := frameworks.Listen("tcp", addr)
		if err != nil {
			logging.Fatalf("Listen failed: %v", err)
		}
		lns = append(lns, ln)
		go func() {
			logging.Printf("server exit: %v", server.RunListener(ln))
		}()
	}
	return lns
}

type Handler struct {
	gws.BuiltinEventHandler
}

func (h *Handler) OnOpen(c *gws.Conn) {
	frameworks.SetNoDelay(c.NetConn(), *nodelay)
	c.SetReadDeadline(time.Time{})
}

func (h *Handler) OnMessage(c *gws.Conn, message *gws.Message) {
	c.WriteAsync(message.Opcode, message.Bytes(), func(err error) {
		message.Close()
	})
	// _ = message.Close()
}
