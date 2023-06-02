package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"

	"github.com/fasthttp/websocket"
	reuseport "github.com/libp2p/go-reuseport"
)

var (
	readBufferSize    = flag.Int("b", 1024, `read buffer size`)
	maxReadBufferSize = flag.Int("mrb", 4096, `max read buffer size`)
	_                 = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_                 = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)

	upgrader = websocket.Upgrader{}
)

func main() {
	flag.Parse()

	if *readBufferSize > *maxReadBufferSize {
		log.Printf("readBufferSize: %v, will handle reading by ReadMessage()", *readBufferSize)
	} else {
		log.Printf("readBufferSize: %v, will handle reading by NextReader()", *readBufferSize)
	}

	addrs, err := config.GetFrameworkServerAddrs(config.Fasthttp)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Fasthttp, err)
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
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	c.SetReadDeadline(time.Time{})
	defer c.Close()

	// avoid connections hold large buffer
	if *readBufferSize > *maxReadBufferSize {
		for {
			mt, message, err := c.ReadMessage()
			if err != nil {
				log.Printf("read message failed: %v", err)
				return
			}
			err = c.WriteMessage(mt, message)
			if err != nil {
				log.Printf("write failed: %v", err)
				return
			}
		}
	}

	buffer := make([]byte, *readBufferSize)
	for {
		mt, reader, err := c.NextReader()
		if err != nil {
			log.Printf("read failed: %v", err)
			return
		}
		n, err := io.ReadAtLeast(reader, buffer, *readBufferSize)
		if err != nil || n <= 0 {
			log.Printf("read at least failed: %v, %v", n, err)
			break
		}
		err = c.WriteMessage(mt, buffer[:n])
		if err != nil {
			log.Printf("write failed: %v", err)
			return
		}
	}
}
