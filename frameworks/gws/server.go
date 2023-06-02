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

	"github.com/libp2p/go-reuseport"
	"github.com/lxzan/gws"
)

var (
	_ = flag.Int("b", 1024, `read buffer size`)
	_ = flag.Int("mrb", 4096, `max read buffer size`)
	_ = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_ = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)

	chExit = make(chan struct{})
)

func main() {
	flag.Parse()

	// ports := strings.Split(config.Ports[config.Gws], ":")
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
	addrs, err := config.GetFrameworkServerAddrs(config.Gws)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Gws, err)
	}
	startServers(addrs)
	pidServerAddr, err := config.GetFrameworkPidServerAddrs(config.Gws)
	if err != nil {
		logging.Fatalf("GetFrameworkPidServerAddrs(%v) failed: %v", config.Gws, err)
	}
	go func() {
		mux := &http.ServeMux{}
		mux.HandleFunc("/pid", onServerPid)
		log.Fatalf("pid server exit: %v", http.ListenAndServe(pidServerAddr, mux))
	}()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	close(chExit)
}

func startServers(addrs []string) {
	for _, v := range addrs {
		go func(addr string) {
			server := gws.NewServer(new(Handler), &gws.ServerOption{})
			ln, err := reuseport.Listen("tcp", addr)
			if err != nil {
				logging.Fatalf("Listen failed: %v", err)
			}
			go func() {
				<-chExit
				ln.Close()
			}()
			logging.Fatalf("server exit: %v", server.RunListener(ln))
		}(v)
	}
}

func onServerPid(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%d", os.Getpid())
}

type Handler struct {
	gws.BuiltinEventHandler
}

func (h *Handler) OnOpen(c *gws.Conn) {
	c.SetReadDeadline(time.Time{})
}

func (h *Handler) OnMessage(c *gws.Conn, message *gws.Message) {
	defer message.Close()
	_ = c.WriteMessage(message.Opcode, message.Bytes())
}
