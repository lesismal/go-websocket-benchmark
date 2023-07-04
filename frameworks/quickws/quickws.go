package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"

	//"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"

	"github.com/antlabs/quickws"
)

var (
	_ = flag.Int("b", 1024, `read buffer size`)
	_ = flag.Int("mrb", 4096, `max read buffer size`)
	_ = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_ = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
)

func main() {
	flag.Parse()

	addrs, err := config.GetFrameworkServerAddrs(config.Quickws)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Quickws, err)
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
	c, err := quickws.Upgrade(w, r,
		// quickws.WithServerDecompression(),
		// quickws.WithServerIgnorePong(),
		quickws.WithWindowsMultipleTimesPayloadSize(2.0),
		quickws.WithServerCallback(&Handler{}),
		// quickws.WithServerReadTimeout(5*time.Second),
	)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	// c.SetReadDeadline(time.Time{})
	c.StartReadLoop()
}

type Handler struct {
	quickws.DefCallback
}

func (h *Handler) OnMessage(c *quickws.Conn, op quickws.Opcode, msg []byte) {
	_ = c.WriteMessage(op, msg)
}
