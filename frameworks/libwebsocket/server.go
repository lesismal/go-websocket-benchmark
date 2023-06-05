package main

import (
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"

	websocket "github.com/shuLhan/share/lib/websocket"
)

func main() {
	var (
		servers []*websocket.Server
		addrs   []string
		err     error
	)
	addrs, err = config.GetFrameworkServerAddrs(config.Libwebsocket)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Libwebsocket, err)
	}
	servers = startServers(addrs)

	var interrupt = make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGQUIT, syscall.SIGSEGV, syscall.SIGTERM, syscall.SIGINT)
	<-interrupt
	stopServers(servers)
}

func startServers(addrs []string) (servers []*websocket.Server) {
	var (
		serverq = make(chan *websocket.Server, 1)
		addr    string
	)

	for _, addr = range addrs {
		go func(addr string) {
			var (
				opts = &websocket.ServerOptions{
					Address:      addr,
					ConnectPath:  `/ws`,
					StatusPath:   `/pid`,
					HandleBin:    handleBin,
					HandleStatus: handleStatus,
					HandleText:   handleText,
				}
				srv = websocket.NewServer(opts)
			)

			serverq <- srv
			log.Printf(`Start server at %s`, addr)
			srv.Start()
		}(addr)
		var srv = <-serverq
		servers = append(servers, srv)
	}
	return servers
}

func stopServers(servers []*websocket.Server) {
	var srv *websocket.Server
	for _, srv = range servers {
		srv.Stop()
	}
}

func handleBin(conn int, payload []byte) {
	var packet = websocket.NewFrameBin(false, payload)

	var err = websocket.Send(conn, packet)
	if err != nil {
		log.Println(`handleBin: ` + err.Error())
	}
}

func handleStatus() (contentType string, data []byte) {
	var (
		pid    = os.Getpid()
		strPid = strconv.Itoa(pid)
	)
	contentType = `text/plain`
	return contentType, []byte(strPid)
}

func handleText(conn int, payload []byte) {
	var packet = websocket.NewFrameText(false, payload)

	var err = websocket.Send(conn, packet)
	if err != nil {
		log.Println(`handleText: ` + err.Error())
	}
}
