package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"go-websocket-benchmark/conf"

	"github.com/fasthttp/websocket"
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

	ports := strings.Split(conf.Ports[conf.FasthttpWS], ":")
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
			mux.HandleFunc("/pid", onServerPid)
			server := http.Server{
				Addr:    addr,
				Handler: mux,
			}
			log.Fatalf("server exit: %v", server.ListenAndServe())
		}(v)
	}
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
