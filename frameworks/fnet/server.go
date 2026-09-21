package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"

	"github.com/linfeip/fnet"
	"github.com/linfeip/fnet/websocket"
)

var (
	nodelay = flag.Bool("nodelay", true, `tcp nodelay`)
	_       = flag.Int("b", 1024, `read buffer size`)
	_       = flag.Int("mrb", 4096, `max read buffer size`)
	_       = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_       = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_       = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)

	upgrader = &websocket.Upgrader{
		OnMessage: func(c *websocket.Conn, op websocket.OpCode, msg []byte) {
			_ = c.WriteMessage(op, msg)
		},
	}
)

func main() {
	flag.Parse()

	addrs, err := config.GetFrameworkServerAddrs(config.Fnet)
	if err != nil {
		logging.Fatalf("GetFrameworkServerAddrs(%v) failed: %v", config.Fnet, err)
	}
	server := startServer(addrs)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	_ = server.Close()
}

// startServer runs ONE fnet.Server listening on every address so that all
// ports share a single accept loop and one reactor pool (GOMAXPROCS pollers),
// instead of 50 servers x (1 + GOMAXPROCS) pollers.
func startServer(addrs []string) *fnet.Server {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)
	frameworks.HandleCommon(mux)
	s := &fnet.Server{
		Addrs:   addrs,
		Handler: mux,
		Listen:  frameworks.Listen,
	}
	go func() {
		logging.Printf("server exit: %v", s.ListenAndServe())
	}()
	return s
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	frameworks.SetNoDelay(c.NetConn(), *nodelay)
	c.SetReadDeadline(time.Time{})
}
