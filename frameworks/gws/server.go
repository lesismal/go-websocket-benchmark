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

	"github.com/libp2p/go-reuseport"
	"github.com/lxzan/gws"
)

var (
	_ = flag.Int("b", 1024, `read buffer size`)
	_ = flag.Int("mrb", 4096, `max read buffer size`)
	_ = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_ = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
)

func main() {
	flag.Parse()

	addrs, err := config.GetFrameworkServerAddrs(config.Gws)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Gws, err)
	}
	lns := startServers(addrs)
	pidServerAddr, err := config.GetFrameworkPidServerAddrs(config.Gws)
	if err != nil {
		logging.Fatalf("GetFrameworkPidServerAddrs(%v) failed: %v", config.Gws, err)
	}
	var pidLn net.Listener
	go func() {
		mux := &http.ServeMux{}
		mux.HandleFunc("/pid", onServerPid)
		ln, err := reuseport.Listen("tcp", pidServerAddr)
		if err != nil {
			logging.Fatalf("Listen failed: %v", err)
		}
		pidLn = ln
		log.Printf("pid server exit: %v", http.Serve(ln, mux))
	}()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	for _, ln := range lns {
		ln.Close()
	}
	pidLn.Close()
}

func startServers(addrs []string) []net.Listener {
	lns := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		server := gws.NewServer(new(Handler), &gws.ServerOption{})
		ln, err := reuseport.Listen("tcp", addr)
		if err != nil {
			logging.Fatalf("Listen failed: %v", err)
		}
		lns = append(lns, ln)
		go func() {
			logging.Printf("server exit: %v", server.RunListener(ln))
		}()
	}
	return lns
}

func onServerPid(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%d", os.Getpid())
}

type Handler struct {
	gws.BuiltinEventHandler
}

func (h *Handler) OnOpen(c *gws.Conn) {
	c.SetReadDeadline(time.Time{})
}

func (h *Handler) OnMessage(c *gws.Conn, message *gws.Message) {
	defer message.Close()
	_ = c.WriteMessage(message.Opcode, message.Bytes())
}
