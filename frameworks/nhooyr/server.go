package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"go-websocket-benchmark/conf"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"

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

	ports := strings.Split(conf.Ports[conf.Nhooyr], ":")
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

	var buffer = bytes.NewBufferString("")
	var tmp = make([]byte, 4096)
	for {
		mt, data, err := c.Reader(context.Background())
		if err != nil {
			log.Printf("read failed: %v", err)
			break
		}

		buffer.Reset()
		io.CopyBuffer(buffer, data, tmp)
		err = c.Write(context.Background(), mt, buffer.Bytes())
		if err != nil {
			log.Printf("write failed: %v", err)
			return
		}
	}
}
