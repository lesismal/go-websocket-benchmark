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

	upgrader = gws.NewUpgrader(&Handler{}, &gws.ServerOption{})
)

func main() {
	flag.Parse()

	addrs, err := config.GetFrameworkServerAddrs(config.GwsStd)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.GwsStd, err)
	}
	lns := startServers(addrs)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	for _, ln := range lns {
		ln.Close()
	}
}

func startServers(addrs []string) []net.Listener {
	lns := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		mux := &http.ServeMux{}
		mux.HandleFunc("/ws", onWebsocket)
		frameworks.HandleCommon(mux)
		server := http.Server{
			// Addr:    addr,
			Handler: mux,
		}
		ln, err := frameworks.Listen("tcp", addr)
		if err != nil {
			logging.Fatalf("Listen failed: %v", err)
		}
		lns = append(lns, ln)
		go func() {
			logging.Printf("server exit: %v", server.Serve(ln))
		}()
	}
	return lns
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	frameworks.SetNoDelay(c.NetConn(), *nodelay)
	c.SetReadDeadline(time.Time{})
	go func() {
		c.ReadLoop()
	}()
}

type Handler struct {
	gws.BuiltinEventHandler
}

func (h *Handler) OnMessage(c *gws.Conn, message *gws.Message) {
	_ = c.WriteMessage(message.Opcode, message.Bytes())
	_ = message.Close()
}
