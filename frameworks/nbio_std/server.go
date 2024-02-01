package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"

	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	nodelay = flag.Bool("nodelay", true, `tcp nodelay`)
	payload = flag.Int("b", 1024, `read buffer size`)
	_       = flag.Int("mrb", 4096, `max read buffer size`)
	_       = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_       = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)

	upgrader = websocket.NewUpgrader()
)

func main() {
	flag.Parse()

	mempool.DefaultMemPool = mempool.NewAligned()
	// debug.SetMemoryLimit(1024 * 1024 * 512)

	ch := make(chan func(), 100)
	for i := 0; i < 100; i++ {
		go func() {
			for f := range ch {
				f()
			}
		}()
	}
	goFunc := func(f func()) {
		ch <- f
	}
	engine := nbhttp.NewEngine(nbhttp.Config{
		ReleaseWebsocketPayload: true,
		// EpollMod:                nbio.EPOLLET,
		// EPOLLONESHOT:            nbio.EPOLLONESHOT,
		ServerExecutor: goFunc,
	})
	err := engine.Start()
	if err != nil {
		panic(err)
	}
	upgrader.Engine = engine
	upgrader.KeepaliveTime = 0
	upgrader.BlockingModAsyncWrite = false

	upgrader.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		c.WriteMessage(messageType, data)
	})

	addrs, err := config.GetFrameworkServerAddrs(config.NbioStd)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.NbioStd, err)
	}
	lns := startServers(addrs)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt

	for _, ln := range lns {
		ln.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	engine.Shutdown(ctx)
}

func startServers(addrs []string) []net.Listener {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)
	mux.HandleFunc("/pid", onServerPid)
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	lns := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
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

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.UpgradeAndTransferConnToPoller(w, r, nil)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	frameworks.SetNoDelay(c.Conn, *nodelay)
	c.SetReadDeadline(time.Time{})
}
