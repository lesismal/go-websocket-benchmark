package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"go-websocket-benchmark/conf"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

var (
	_ = flag.Int("nb", 10000, `max blocking online num, e.g. 10000`)
	_ = flag.Int("b", 1024, `read buffer size`)
)

func main() {
	flag.Parse()

	ports := strings.Split(conf.Ports[conf.Gobwas], ":")
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
	c, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Fatalf("UpgradeHTTP failed: %v", err)
	}

	c.SetReadDeadline(time.Time{})
	go func() {
		defer c.Close()
		for {
			msg, op, err := wsutil.ReadClientData(c)
			if err != nil {
				log.Fatalf("read failed: %v", err)
			}
			err = wsutil.WriteServerMessage(c, op, msg)
			if err != nil {
				log.Fatalf("write failed: %v", err)
			}
		}
	}()
}
