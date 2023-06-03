package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"

	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
	"github.com/libp2p/go-reuseport"
)

var (
	_ = flag.Int("b", 1024, `read buffer size`)
	_ = flag.Int("mrb", 4096, `max read buffer size`)
	_ = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_ = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)

	upgrader = websocket.NewUpgrader()
)

func main() {
	flag.Parse()

	upgrader.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		c.WriteMessage(messageType, data)
	})
	upgrader.BlockingModAsyncWrite = false

	addrs, err := config.GetFrameworkServerAddrs(config.NbioModBlocking)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.NbioModBlocking, err)
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
	mux.HandleFunc("/pid", onServerPid)
	engine := nbhttp.NewEngine(nbhttp.Config{
		Network:                 "tcp",
		Addrs:                   addrs,
		Handler:                 mux,
		IOMod:                   nbhttp.IOModBlocking,
		ReleaseWebsocketPayload: true,
		Listen:                  reuseport.Listen,
	})

	err := engine.Start()
	if err != nil {
		logging.Fatalf("nbio.Start failed: %v", err)
	}

	return engine
}

func onServerPid(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%d", os.Getpid())
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	c.SetReadDeadline(time.Time{})
}
