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
	"runtime"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"

	//"time"

	"github.com/antlabs/greatws"
)

var (
	nodelay = flag.Bool("nodelay", true, `tcp nodelay`)
	_       = flag.Int("b", 1024, `read buffer size`)
	_       = flag.Int("mrb", 4096, `max read buffer size`)
	_       = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_       = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
)

var upgrader *greatws.UpgradeServer

func main() {
	flag.Parse()

	var h Handler
	h.m = greatws.NewMultiEventLoopMust(
		greatws.WithEventLoops(runtime.NumCPU()), // 控制io go程数
		greatws.WithBusinessGoNum(80, 100, 80),   // 控制业务go程数, 默认启动100个, 最小100个，最大10000个
		greatws.WithMaxEventNum(1000),
		greatws.WithLogLevel(slog.LevelError)) // epoll, kqueue
	h.m.Start()
	opt := []greatws.ServerOption{
		// greatws.WithServerIgnorePong(),
		greatws.WithServerCallback(&Handler{}),
		greatws.WithServerMultiEventLoop(h.m),
		greatws.WithServerCallbackInEventLoop(),
	}

	if !*nodelay {
		opt = append(opt, greatws.WithServerTCPDelay())
	}
	upgrader = greatws.NewUpgrade(opt...)

	addrs, err := config.GetFrameworkServerAddrs(config.GreatwsEvent)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.GreatwsEvent, err)
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
	greatws.DefCallback
	m *greatws.MultiEventLoop
}

func (h *Handler) OnMessage(c *greatws.Conn, op greatws.Opcode, msg []byte) {
	_ = c.WriteMessage(op, msg)
}
