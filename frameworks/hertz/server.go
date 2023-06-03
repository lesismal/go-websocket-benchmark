package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/hertz-contrib/websocket"
)

var (
	readBufferSize    = flag.Int("b", 1024, `read buffer size`)
	maxReadBufferSize = flag.Int("mrb", 4096, `max read buffer size`)
	_                 = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_                 = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)

	upgrader = websocket.HertzUpgrader{}
)

func main() {
	flag.Parse()

	gopool.SetCap(1000000)

	if *readBufferSize > *maxReadBufferSize {
		log.Printf("readBufferSize: %v, will handle reading by ReadMessage()", *readBufferSize)
	} else {
		log.Printf("readBufferSize: %v, will handle reading by NextReader()", *readBufferSize)
	}

	addrs, err := config.GetFrameworkServerAddrs(config.Hertz)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Hertz, err)
	}
	srvs := startServers(addrs)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	for _, srv := range srvs {
		srv.Close()
	}
}

func startServers(addrs []string) []*server.Hertz {
	srvs := make([]*server.Hertz, 0, len(addrs))
	for _, addr := range addrs {
		srv := server.New(server.WithHostPorts(addr))
		srvs = append(srvs, srv)
		go func() {
			srv.GET("/ws", onWebsocket)
			srv.GET("/pid", onServerPid)
			srv.Spin()
		}()
	}
	return srvs
}

func onServerPid(c context.Context, ctx *app.RequestContext) {
	ctx.Response.BodyWriter().Write([]byte(fmt.Sprintf("%d", os.Getpid())))
}

func onWebsocket(c context.Context, ctx *app.RequestContext) {
	upgradeErr := upgrader.Upgrade(ctx, func(c *websocket.Conn) {
		_ = c.SetReadDeadline(time.Time{})
		defer c.Close()

		// avoid connections hold large buffer
		if *readBufferSize > *maxReadBufferSize {
			for {
				mt, message, err := c.ReadMessage()
				if err != nil {
					// log.Printf("read message failed: %v", err)
					return
				}
				err = c.WriteMessage(mt, message)
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
			mt, reader, err := c.NextReader()
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
			err = c.WriteMessage(mt, readBuffer[:nread])
			nread = 0
			if err != nil {
				// log.Printf("write failed: %v", err)
				return
			}
		}
	})

	if upgradeErr != nil {
		log.Printf("upgrade failed: %v", upgradeErr)
		return
	}
}
