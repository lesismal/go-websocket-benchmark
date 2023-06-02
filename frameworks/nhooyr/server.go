package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"

	"github.com/libp2p/go-reuseport"
	"nhooyr.io/websocket"
)

var (
	readBufferSize    = flag.Int("b", 1024, `read buffer size`)
	maxReadBufferSize = flag.Int("mrb", 4096, `max read buffer size`)
	_                 = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_                 = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
)

func main() {
	flag.Parse()

	if *readBufferSize > *maxReadBufferSize {
		log.Printf("readBufferSize: %v, will handle reading by ReadMessage()", *readBufferSize)
	} else {
		log.Printf("readBufferSize: %v, will handle reading by NextReader()", *readBufferSize)
	}

	addrs, err := config.GetFrameworkServerAddrs(config.Nhooyr)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Nhooyr, err)
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
			Addr:    addr,
			Handler: mux,
		}
		ln, err := reuseport.Listen("tcp", addr)
		if err != nil {
			logging.Fatalf("Listen failed: %v", err)
		}
		lns = append(lns, ln)
		go logging.Fatalf("server exit: %v", server.Serve(ln))
	}
	return lns
}

func onServerPid(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%d", os.Getpid())
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusInternalError, "the sky is falling")

	if *readBufferSize > *maxReadBufferSize {
		for {
			mt, data, err := c.Read(context.Background())
			if err != nil {
				log.Printf("read failed: %v", err)
				return
			}
			err = c.Write(context.Background(), mt, data)
			if err != nil {
				log.Printf("write failed: %v", err)
				return
			}
		}
	}

	buffer := make([]byte, *readBufferSize)
	for {
		mt, reader, err := c.Reader(context.Background())
		if err != nil {
			log.Printf("read failed: %v", err)
			break
		}
		// Here assume the ws message coming is the same size as the `readBuffer`;
		// This is to help to increase the framework's benchmark report as high as possible;
		// But it's not fair to others considering real scenarios.
		n, err := io.ReadAtLeast(reader, buffer, *readBufferSize)
		if err != nil || n <= 0 {
			log.Printf("read at least failed: %v, %v", n, err)
			break
		}
		err = c.Write(context.Background(), mt, buffer[:n])
		if err != nil {
			log.Printf("write failed: %v", err)
			return
		}
	}
}
