package main

import (
	"flag"
	"fmt"
	"go-websocket-benchmark/conf"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/lxzan/gws"
)

var (
	_ = flag.Int("nb", 10000, `max blocking online num, e.g. 10000`)
	_ = flag.Int("b", 1024, `read buffer size`)
)

func main() {
	flag.Parse()

	ports := strings.Split(conf.Ports[conf.Gws], ":")
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
			server := gws.NewServer(new(Handler), &gws.ServerOption{})
			log.Fatalf("server exit: %v", server.Run(addr))
		}(v)
	}
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
