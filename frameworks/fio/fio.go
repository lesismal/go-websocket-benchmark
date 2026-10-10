package main

import (
	"flag"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/taskpool"

	//"time"

	// 这个库改名了（greatws -> quicknet -> fio），代码也重组进了
	// websocket/ 子目录，包的正式名字是 websocket。别名成 fio，
	// 和这个框架在压测里的名字保持一致。
	fio "github.com/antlabs/fio/websocket"
)

var (
	nodelay = flag.Bool("nodelay", true, `tcp nodelay`)
	_       = flag.Int("b", 1024, `read buffer size`)
	_       = flag.Int("mrb", 4096, `max read buffer size`)
	_       = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_       = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_       = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)

	// fio.WithEventLoops. 0 leaves fio's own default, one per CPU.
	// script/server.sh sets it from BENCH_EVENTLOOPS in script/config.sh.
	eventLoops = flag.Int("eventloops", 0, `event loops, 0 for fio's own default`)
)

var upgrader *fio.UpgradeServer

func main() {
	flag.Parse()

	// fio picks the task pool its callbacks run on by name, so a shared
	// pool goes in as a task driver of its own. It has to be registered
	// before the event loops are built, since fio instantiates every
	// registered driver for each of them.
	pool := taskpool.FromFlags()
	taskMode := ""
	if pool != nil {
		taskMode = taskpool.RegisterFioTaskDriver(pool)
	}

	var h Handler
	h.m = fio.NewMultiEventLoopMust(
		fio.WithEventLoops(*eventLoops),    // 控制io go程数
		fio.WithBusinessGoNum(80, 100, 80), // 控制业务go程数, 默认启动100个, 最小100个，最大10000个
		fio.WithMaxEventNum(1000),
		fio.WithLogLevel(slog.LevelError)) // epoll, kqueue
	h.m.Start()
	logging.Printf("%v server: eventloops=%d (0 = fio's own default)", config.Fio, *eventLoops)
	opt := []fio.ServerOption{
		// fio.WithServerIgnorePong(),
		fio.WithServerCallback(&Handler{}),
		fio.WithServerMultiEventLoop(h.m),
	}
	if taskMode != "" {
		opt = append(opt, fio.WithServerCustomTaskMode(taskMode))
	}

	// fio v0.2.2 stores this option but never applies it; the
	// server's ConnState below is what sets TCP_NODELAY. It is still
	// passed so that a fio which starts applying it agrees with that
	// rather than putting its own default back.
	if !*nodelay {
		opt = append(opt, fio.WithServerTCPDelay())
	}
	upgrader = fio.NewUpgrade(opt...)

	addrs, err := config.GetFrameworkServerAddrs(config.Fio)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Fio, err)
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
		frameworks.HandleCommon(mux)
		server := http.Server{
			// Addr:    addr,
			Handler: mux,
			// fio takes the socket over by duplicating the hijacked
			// conn's fd, so setting it on that conn sets it on the socket
			// fio serves.
			ConnState: func(c net.Conn, state http.ConnState) {
				if state == http.StateHijacked {
					frameworks.SetNoDelay(c, *nodelay)
				}
			},
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
	fio.DefCallback
	m *fio.MultiEventLoop
}

func (h *Handler) OnMessage(c *fio.Conn, op fio.Opcode, msg []byte) {
	_ = c.WriteMessage(op, msg)
}
