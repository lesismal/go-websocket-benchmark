package main

import (
	"context"
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

	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	nodelay  = flag.Bool("nodelay", true, `tcp nodelay`)
	payload  = flag.Int("b", 1024, `read buffer size`)
	memLimit = flag.Int64("m", 1024*1024*1024*1, `memory limit`)
	_        = flag.Int("mrb", 4096, `max read buffer size`)
	_        = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_        = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)

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

	addrs, err := config.GetFrameworkServerAddrs(config.NbioModNonblocking)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.NbioModNonblocking, err)
	}
	engine := startServers(addrs)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()
	engine.Shutdown(ctx)
}

func startServers(addrs []string) *nbhttp.Engine {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)
	frameworks.HandleCommon(mux)
	engine := nbhttp.NewEngine(nbhttp.Config{
		Network:                 "tcp",
		Addrs:                   addrs,
		Handler:                 mux,
		IOMod:                   nbhttp.IOModNonBlocking,
		ReleaseWebsocketPayload: true,
		Listen:                  frameworks.Listen,
	})

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
	frameworks.SetNoDelay(c.Conn, *nodelay)
	c.SetReadDeadline(time.Time{})
}
