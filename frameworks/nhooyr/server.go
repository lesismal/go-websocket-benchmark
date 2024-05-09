package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"

	"nhooyr.io/websocket"
)

var (
	nodelay           = flag.Bool("nodelay", true, `tcp nodelay`)
	readBufferSize    = flag.Int("b", 1024, `read buffer size`)
	maxReadBufferSize = flag.Int("mrb", 4096, `max read buffer size`)
	_                 = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_                 = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_                 = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)
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
		frameworks.HandleCommon(mux)
		server := http.Server{
			Addr:    addr,
			Handler: mux,
			ConnState: func(c net.Conn, state http.ConnState) {
				if http.StateHijacked == state {
					frameworks.SetNoDelay(c, *nodelay)
				}
			},
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

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	// frameworks.SetNoDelay(c.Reader(context.Background())., *nodelay)
	defer c.Close(websocket.StatusInternalError, "the sky is falling")

	if *readBufferSize > *maxReadBufferSize {
		for {
			mt, data, err := c.Read(context.Background())
			if err != nil {
				// log.Printf("read failed: %v", err)
				return
			}
			err = c.Write(context.Background(), mt, data)
			if err != nil {
				// log.Printf("write failed: %v", err)
				return
			}
		}
	}

	var nread int
	var buffer = make([]byte, *readBufferSize)
	var readBuffer = buffer
	for {
		mt, reader, err := c.Reader(context.Background())
		if err != nil {
			// log.Printf("read failed: %v", err)
			return
		}
		for {
			if nread == len(readBuffer) {
				readBuffer = append(readBuffer, buffer...)
			}
			n, err := reader.Read(readBuffer[nread:])
			nread += n
			if err == io.EOF {
				break
			}
		}
		err = c.Write(context.Background(), mt, readBuffer[:nread])
		nread = 0
		if err != nil {
			// log.Printf("write failed: %v", err)
			return
		}
	}
}
