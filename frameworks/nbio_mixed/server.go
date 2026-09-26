package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/taskpool"

	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	nodelay           = flag.Bool("nodelay", true, `tcp nodelay`)
	payload           = flag.Int("b", 1024, `read buffer size`)
	memLimit          = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	maxBlockingOnline = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_                 = flag.Int("mrb", 4096, `max read buffer size`)
	_                 = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)

	upgrader = websocket.NewUpgrader()
)

func main() {
	flag.Parse()

	debug.SetMemoryLimit(*memLimit)

	mempool.DefaultMemPool = mempool.New(*payload+1024, 1024*1024*1024)

	upgrader.OnMessagePtr(func(c *websocket.Conn, messageType websocket.MessageType, pdata *[]byte) {
		c.WriteMessage(messageType, *pdata)
	})
	upgrader.KeepaliveTime = 0
	upgrader.BlockingModAsyncWrite = false

	addrs, err := config.GetFrameworkServerAddrs(config.NbioModMixed)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.NbioModMixed, err)
	}
	engine := startServers(addrs)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	engine.Stop()
}

func startServers(addrs []string) *nbhttp.Engine {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)
	frameworks.HandleCommon(mux)
	engineConfig := nbhttp.Config{
		Network:                 "tcp",
		Addrs:                   addrs,
		Handler:                 mux,
		IOMod:                   nbhttp.IOModMixed,
		MaxBlockingOnline:       *maxBlockingOnline,
		ReleaseWebsocketPayload: true,
		Listen:                  frameworks.Listen,
	}

	// nbio runs the reading callbacks on a task pool of its own sizing.
	// -taskpool hands that work to one of the shared pools instead, so that
	// nbio can be measured on another framework's scheduler.
	if pool := taskpool.FromFlags(); pool != nil {
		engineConfig.ServerExecutor = taskpool.NbioExecute(pool)
	}

	engine := nbhttp.NewEngine(engineConfig)

	err := engine.Start()
	if err != nil {
		log.Fatalf("nbio.Start failed: %v", err)
	}

	return engine
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	// A conn nbhttp's engine accepted is its *nbhttp.Conn, which has no
	// SetNoDelay of its own: set it on the socket's conn inside.
	conn := c.Conn
	if hc, ok := conn.(*nbhttp.Conn); ok {
		conn = hc.Conn
	}
	frameworks.SetNoDelay(conn, *nodelay)
	c.SetReadDeadline(time.Time{})
}
