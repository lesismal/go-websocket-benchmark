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

	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	_ = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_ = flag.Int("b", 1024, `read buffer size`)
	_ = flag.Int64("m", 1024*1024*1024*2, `memory limit`)

	upgrader = websocket.NewUpgrader()
)

func main() {
	flag.Parse()

	upgrader.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		c.WriteMessage(messageType, data)
	})

	ports := strings.Split(conf.Ports[conf.NbioBasedonStdhttp], ":")
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
	for _, v := range addrs {
		go func(addr string) {
			mux := &http.ServeMux{}
			mux.HandleFunc("/ws", onWebsocket)
			server := http.Server{
				Addr:    addr,
				Handler: mux,
			}
			log.Fatalf("server exit: %v", server.ListenAndServe())
		}(v)
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
