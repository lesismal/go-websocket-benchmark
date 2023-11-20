package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"

	//"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"

	"github.com/antlabs/bigws"
)

var (
	nodelay = flag.Bool("nodelay", true, `tcp nodelay`)
	_       = flag.Int("b", 1024, `read buffer size`)
	_       = flag.Int("mrb", 4096, `max read buffer size`)
	_       = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_       = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
)

var upgrader *bigws.UpgradeServer

func main() {
	flag.Parse()

	var h Handler
	h.m = bigws.NewMultiEventLoopMust(bigws.WithEventLoops(0), bigws.WithMaxEventNum(1000), bigws.WithLogLevel(slog.LevelError)) // epoll, kqueue
	h.m.Start()
	opt := []bigws.ServerOption{
		// bigws.WithServerIgnorePong(),
		bigws.WithServerCallback(&Handler{}),
		bigws.WithServerMultiEventLoop(h.m),
	}

	if !*nodelay {
		opt = append(opt, bigws.WithServerTCPDelay())
	}
	upgrader = bigws.NewUpgrade(opt...)

	addrs, err := config.GetFrameworkServerAddrs(config.Bigws)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Bigws, err)
	}

	lns := h.startServers(addrs)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	for _, ln := range lns {
		ln.Close()
	}
}

func (h *Handler) startServers(addrs []string) []net.Listener {
	lns := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		mux := &http.ServeMux{}
		mux.HandleFunc("/ws", h.onWebsocket)
		mux.HandleFunc("/pid", onServerPid)
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

func onServerPid(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%d", os.Getpid())
}

func (h *Handler) onWebsocket(w http.ResponseWriter, r *http.Request) {
	_, err := upgrader.Upgrade(w, r)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	// c.SetDeadline(time.Time{})
	// c.StartReadLoop()
}

type Handler struct {
	bigws.DefCallback
	m *bigws.MultiEventLoop
}

func (h *Handler) OnMessage(c *bigws.Conn, op bigws.Opcode, msg []byte) {
	_ = c.WriteMessage(op, msg)
}
