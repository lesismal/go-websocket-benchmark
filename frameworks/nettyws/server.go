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

	// ports := strings.Split(config.Ports[config.GoNettyWs], ":")
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
	addrs, err := config.GetFrameworkServerAddrs(config.GoNettyWs)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.GoNettyWs, err)
	}
	startServers(addrs)
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
}

func startServers(addrs []string) {
	for _, addr := range addrs {
		var serveMux = http.NewServeMux()
		serveMux.HandleFunc("/pid", onServerPid)

		var ws = nettyws.NewWebsocket(
			fmt.Sprintf("%s/ws", addr),
			nettyws.WithServeMux(serveMux),
			nettyws.WithBinary(),
			nettyws.WithBufferSize(2048, 2048),
		)
		ws.OnData = func(conn nettyws.Conn, data []byte) {
			conn.Write(data)
		}

		go func() {
			if err := ws.Listen(); nil != err {
				panic(err)
			}
		}()
	}
}

func onServerPid(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%d", os.Getpid())
}
