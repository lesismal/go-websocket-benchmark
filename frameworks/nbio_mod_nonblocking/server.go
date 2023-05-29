package main

import (
	"flag"
	"fmt"
	"go-websocket-benchmark/conf"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	_ = flag.Int("b", 1024, `read buffer size`)
	_ = flag.Int("nb", 10000, `max blocking online num, e.g. 10000`)

	upgrader = websocket.NewUpgrader()
)

func main() {
	flag.Parse()

	upgrader.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		c.WriteMessage(messageType, data)
	})

	ports := strings.Split(conf.Ports[conf.NbioModNonblocking], ":")
	minPort, err := strconv.Atoi(ports[0])
	if err != nil {
		log.Fatalf("invalid port range: %v, %v", ports, err)
	}
	maxPort, err := strconv.Atoi(ports[1])
	if err != nil {
		log.Fatalf("invalid port range: %v, %v", ports, err)
	}
	addrs := []string{}
	for i := minPort; i <= maxPort; i++ {
		addrs = append(addrs, fmt.Sprintf(":%d", i))
	}
	startServers(addrs)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
}

func startServers(addrs []string) {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)
	svr := nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   addrs,
		Handler: mux,
		IOMod:   nbhttp.IOModNonBlocking,
	})

	err := svr.Start()
	if err != nil {
		log.Printf("nbio.Start failed: %v", err)
		return
	}
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	c.SetReadDeadline(time.Time{})
}
