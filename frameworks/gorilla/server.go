package main

import (
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
	"time"

	"github.com/gorilla/websocket"
)

var (
	_              = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_              = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	readBufferSize = flag.Int("b", 1024, `read buffer size`)

	upgrader = websocket.Upgrader{}
)

func main() {
	flag.Parse()

	ports := strings.Split(conf.Ports[conf.Gorilla], ":")
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
	defer c.Close()

	var nread int
	var buffer = make([]byte, *readBufferSize)
	var readBuffer = buffer
	for {
		mt, reader, err := c.NextReader()
		if err != nil {
			log.Fatalf("read failed: %v", err)
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
		err = c.WriteMessage(mt, readBuffer[:nread])
		nread = 0
		if err != nil {
			log.Fatalf("write failed: %v", err)
		}
	}
}
