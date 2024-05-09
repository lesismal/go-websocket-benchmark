package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/network/standard"
	"github.com/hertz-contrib/pprof"
	"github.com/hertz-contrib/websocket"
	"github.com/lesismal/perf"
)

var (
	nodelay           = flag.Bool("nodelay", true, `tcp nodelay`)
	readBufferSize    = flag.Int("b", 1024, `read buffer size`)
	maxReadBufferSize = flag.Int("mrb", 4096, `max read buffer size`)
	_                 = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_                 = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_                 = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)

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

	addrs, err := config.GetFrameworkServerAddrs(config.HertzStd)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.HertzStd, err)
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
		srv := server.New(server.WithHostPorts(addr),
			server.WithTransport(standard.NewTransporter))
		pprof.Register(srv)
		srvs = append(srvs, srv)
		go func() {
			psCounter, err := perf.NewPSCounter(os.Getpid())
			if err != nil {
				logging.Fatalf("perf.NewPSCounter failed: %v", err)
			}

			srv.GET("/ws", onWebsocket)
			srv.POST("/init", func(c context.Context, ctx *app.RequestContext) {
				body, err := ctx.Body()
				if err != nil {
					logging.Fatalf("perf.NewPSCounter failed: %v", err)
					return
				}
				var args config.InitArgs
				json.Unmarshal(body, &args)
				go func() {
					psCounter.Start(perf.PSCountOptions{
						CountCPU: true,
						CountMEM: true,
						CountIO:  true,
						CountNET: true,
						Interval: args.PsInterval,
					})
					time.Sleep(args.PsInterval)
				}()

				ctx.Response.BodyWriter().Write([]byte(fmt.Sprintf("%d", os.Getpid())))
			})
			srv.GET("/ps", func(c context.Context, ctx *app.RequestContext) {
				b, _ := json.Marshal(psCounter)
				ctx.Response.BodyWriter().Write(b)
			})
			srv.Spin()
		}()
	}
	return srvs
}

func onWebsocket(c context.Context, ctx *app.RequestContext) {
	upgradeErr := upgrader.Upgrade(ctx, func(c *websocket.Conn) {
		frameworks.SetNoDelay(c.NetConn(), *nodelay)
		c.SetReadDeadline(time.Time{})
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
