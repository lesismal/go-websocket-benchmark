package main

import (
	"flag"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"

	"github.com/antlabs/fio/engine"
	fiohttp "github.com/antlabs/fio/http"

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

	// fio's own default is one loop per CPU. A little oversubscription hides
	// the read/write syscall latency; scale it with the CPUs this server
	// actually has (taskset affinity is part of runtime.NumCPU) instead of a
	// fixed count — 16 loops on a 2-core CI runner is 8x too many, and on the
	// 4-core case fio fell behind fnet there (0.50M vs 0.77M echo). 4/3 of
	// NumCPU gives 16 on the 12-core lab server (12 loops 1.70M, 16 loops
	// 1.78M, 20 loops 1.59M) and 5 on 4 cores.
	// -eventloops from BENCH_EVENTLOOPS still overrides it.
	loops := *eventLoops
	if loops == 0 {
		loops = runtime.NumCPU()
		loops += loops / 3
	}

	var h Handler
	h.m = fio.NewMultiEventLoopMust(
		fio.WithEventLoops(loops),          // 控制io go程数
		fio.WithBusinessGoNum(80, 100, 80), // 控制业务go程数, 默认启动100个, 最小100个，最大10000个
		fio.WithMaxEventNum(1000),
		fio.WithLogLevel(slog.LevelError)) // epoll, kqueue
	h.m.Start()
	logging.Printf("%v server: eventloops=%d", config.Fio, loops)
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

func (h *Handler) startServers(addrs []string) []io.Closer {
	lns := make([]io.Closer, 0, len(addrs)+1)
	// fio 的 http 包直接跑在 engine 上：所有数据端口都走它自己的 HTTP/1.1
	// parser + 原生 WebSocket 升级（websocket.UpgradeConn），不再经过标准库
	// net/http 的 per-conn goroutine、bufio、hijack 和 fd dup/close。
	// 控制口（/init /ps /taskpool /debug/pprof）放到端口段后面那个端口，
	// 和 fnet/fib/gws 一样，见 config.GetFrameworkHTTPServerAddrs。
	for _, addr := range addrs {
		newHandler := func() engine.Handler {
			ch := fiohttp.NewConnHandler(fiohttp.HandlerFunc(func(c *fiohttp.Ctx) error {
				c.Status(http.StatusNotFound)
				return nil
			}), 0)
			ch.OnUpgrade(func(c *engine.Conn, r *fiohttp.Request) {
				if _, err := upgrader.UpgradeConn(c, r); err != nil {
					logging.Printf("upgrade failed: %v", err)
				}
			})
			return ch
		}
		ln, err := engine.ListenAndServe(h.m.MultiEventLoop, addr, newHandler)
		if err != nil {
			logging.Fatalf("Listen failed: %v", err)
		}
		lns = append(lns, ln)
	}

	controlAddr, err := config.GetFrameworkHTTPServerAddrs(config.Fio)
	if err != nil {
		logging.Fatalf("GetFrameworkHTTPServerAddrs(%v) failed: %v", config.Fio, err)
	}
	controlMux := &http.ServeMux{}
	frameworks.HandleCommon(controlMux)
	controlLn, err := net.Listen("tcp", controlAddr)
	if err != nil {
		logging.Fatalf("Listen(%v) failed: %v", controlAddr, err)
	}
	controlServer := &http.Server{Handler: controlMux}
	lns = append(lns, controlLn)
	go func() {
		logging.Printf("control server exit: %v", controlServer.Serve(controlLn))
	}()
	return lns
}

type Handler struct {
	fio.DefCallback
	m *fio.MultiEventLoop
}

func (h *Handler) OnMessage(c *fio.Conn, op fio.Opcode, msg []byte) {
	_ = c.WriteMessage(op, msg)
}
