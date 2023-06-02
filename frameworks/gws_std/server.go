package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"

	"github.com/lxzan/gws"
)

var (
	_ = flag.Int("b", 1024, `read buffer size`)
	_ = flag.Int("mrb", 4096, `max read buffer size`)
	_ = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_ = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)

	upgrader = gws.NewUpgrader(&Handler{}, &gws.ServerOption{})
)

func main() {
	flag.Parse()

	// ports := strings.Split(config.Ports[config.GwsBasedonStdhttp], ":")
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
	addrs, err := config.GetFrameworkServerAddrs(config.GwsBasedonStdhttp)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.GwsBasedonStdhttp, err)
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
	c, err := upgrader.Upgrade(w, r)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	c.SetReadDeadline(time.Time{})
	c.ReadLoop()
}

type Handler struct {
	gws.BuiltinEventHandler
}

func (h *Handler) OnMessage(c *gws.Conn, message *gws.Message) {
	defer message.Close()
	_ = c.WriteMessage(message.Opcode, message.Bytes())
}
