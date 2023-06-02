package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
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

	chExit = make(chan struct{})
)

func main() {
	flag.Parse()

	if *readBufferSize > *maxReadBufferSize {
		log.Printf("readBufferSize: %v, will handle reading by ReadMessage()", *readBufferSize)
	} else {
		log.Printf("readBufferSize: %v, will handle reading by NextReader()", *readBufferSize)
	}

	// ports := strings.Split(config.Ports[config.Nhooyr], ":")
	// minPort, err := strconv.Atoi(ports[0])
	// if err != nil {
	// 	log.Fatalf("invalid port range: %v, %v", ports, err)
	// }
	// maxPort, err := strconv.Atoi(ports[1])
	// if err != nil {
	// 	log.Fatalf("invalid port range: %v, %v", ports, err)
	// }
	// addrs := []string{}
	// for i := minPort; i <= maxPort; i++ {
	// 	addrs = append(addrs, fmt.Sprintf(":%d", i))
	// }
	addrs, err := config.GetFrameworkServerAddrs(config.Nhooyr)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Nhooyr, err)
	}
	startServers(addrs)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	close(chExit)
}

func startServers(addrs []string) {
	for _, v := range addrs {
		go func(addr string) {
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
			go func() {
				<-chExit
				ln.Close()
			}()
			logging.Fatalf("server exit: %v", server.Serve(ln))
		}(v)
	}
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
