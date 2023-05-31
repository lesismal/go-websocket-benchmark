package main

import (
	"context"
	"flag"
	"fmt"
	"go-websocket-benchmark/conf"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/hertz-contrib/websocket"
)

var (
	_              = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_              = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	readBufferSize = flag.Int("b", 1024, `read buffer size`)

	upgrader = websocket.HertzUpgrader{}
)

func main() {
	flag.Parse()

	ports := strings.Split(conf.Ports[conf.Hertz], ":")
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
			srv := server.Default(server.WithHostPorts(addr))
			srv.GET("/ws", onWebsocket)
			srv.Spin()
		}(v)
	}
}

func onWebsocket(c context.Context, ctx *app.RequestContext) {
	upgradeErr := upgrader.Upgrade(ctx, func(c *websocket.Conn) {
		_ = c.SetReadDeadline(time.Time{})
		defer c.Close()

		// avoid connections hold large buffer
		if *readBufferSize > 4096 {
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

		var nread int
		var buffer = make([]byte, *readBufferSize)
		var readBuffer = buffer

		for {
			mt, reader, err := c.NextReader()
			if err != nil {
				log.Printf("read failed: %v", err)
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
			err = c.WriteMessage(mt, readBuffer[:nread])
			nread = 0
			if err != nil {
				log.Printf("write failed: %v", err)
				return
			}
		}
	})

	if upgradeErr != nil {
		log.Printf("upgrade failed: %v", upgradeErr)
		return
	}
}
