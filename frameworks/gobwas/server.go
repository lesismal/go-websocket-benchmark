package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/libp2p/go-reuseport"
)

var (
	_ = flag.Int("b", 1024, `read buffer size`)
	_ = flag.Int("mrb", 4096, `max read buffer size`)
	_ = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_ = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
)

func main() {
	flag.Parse()

	addrs, err := config.GetFrameworkServerAddrs(config.Gobwas)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Gobwas, err)
	}
	lns := startServers(addrs)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	for _, ln := range lns {
		ln.Close()
	}
}

func startServers(addrs []string) []net.Listener {
	lns := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		mux := &http.ServeMux{}
		mux.HandleFunc("/ws", onWebsocket)
		mux.HandleFunc("/pid", onServerPid)
		server := http.Server{
			// Addr:    addr,
			Handler: mux,
		}
		ln, err := reuseport.Listen("tcp", addr)
		if err != nil {
			logging.Fatalf("Listen failed: %v", err)
		}
		lns = append(lns, ln)
		go func() {
			logging.Fatalf("server exit: %v", server.Serve(ln))
		}()
	}
	return lns
}

func onServerPid(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%d", os.Getpid())
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
				log.Printf("read failed: %v", err)
				return
			}
			err = wsutil.WriteServerMessage(c, op, msg)
			if err != nil {
				log.Printf("write failed: %v", err)
				return
			}
		}
	}()
}
