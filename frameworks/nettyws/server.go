package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"

	nettyws "github.com/go-netty/go-netty-ws"
)

var (
	_ = flag.Int("b", 1024, `read buffer size`)
	_ = flag.Int("mrb", 4096, `max read buffer size`)
	_ = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_ = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
)

func main() {
	flag.Parse()

	addrs, err := config.GetFrameworkServerAddrs(config.GoNettyWs)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.GoNettyWs, err)
	}
	svrs := startServers(addrs)
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	for _, svr := range svrs {
		svr.Close()
	}
}

func startServers(addrs []string) []*nettyws.Websocket {
	svrs := make([]*nettyws.Websocket, 0, len(addrs))
	for _, addr := range addrs {
		var serveMux = http.NewServeMux()
		serveMux.HandleFunc("/pid", onServerPid)

		var ws = nettyws.NewWebsocket(
			fmt.Sprintf("%s/ws", addr),
			nettyws.WithServeMux(serveMux),
			nettyws.WithBinary(),
			nettyws.WithBufferSize(2048, 2048),
		)
		svrs = append(svrs, ws)
		ws.OnData = func(conn nettyws.Conn, data []byte) {
			conn.Write(data)
		}
		go func() {
			logging.Fatalf("server exit: %v", ws.Listen())
		}()
	}
	return svrs
}

func onServerPid(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%d", os.Getpid())
}
