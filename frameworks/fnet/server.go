package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/taskpool"

	"github.com/linfeip/fnet"
	"github.com/linfeip/fnet/websocket"
)

var (
	nodelay = flag.Bool("nodelay", true, `tcp nodelay`)
	_       = flag.Int("b", 1024, `read buffer size`)
	_       = flag.Int("mrb", 4096, `max read buffer size`)
	_       = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_       = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_       = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)

	upgrader = &websocket.Upgrader{OnMessage: onMessage}
)

// onMessage echoes the frame back.
//
// fnet queues a connection's frames behind one drain and copies each payload
// out of the reactor's read buffer before calling this, so the echo needs
// neither a copy nor an order of its own: msg is fnet's buffer and stays
// valid for the call.
func onMessage(c *websocket.Conn, op websocket.OpCode, msg []byte) {
	_ = c.WriteMessage(op, msg)
}

func main() {
	flag.Parse()

	// fnet runs the websocket callbacks on a worker pool of its own, sharded
	// by connection. -taskpool hands that work to one of the shared pools
	// instead, so that fnet can be measured on another framework's
	// scheduler; -taskpool=default leaves it on its own.
	if pool := taskpool.FromFlags(); pool != nil {
		upgrader.WorkerPool = taskpool.FnetWorkerPool(pool)
	}

	addrs, err := config.GetFrameworkServerAddrs(config.Fnet)
	if err != nil {
		logging.Fatalf("GetFrameworkServerAddrs(%v) failed: %v", config.Fnet, err)
	}
	server := startServer(addrs)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	_ = server.Close()
}

// startServer runs ONE fnet.Server listening on every address so that all
// ports share a single accept loop and one reactor pool (GOMAXPROCS pollers),
// instead of 50 servers x (1 + GOMAXPROCS) pollers.
func startServer(addrs []string) *fnet.Server {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)
	frameworks.HandleCommon(mux)
	s := &fnet.Server{
		Addrs:   addrs,
		Handler: mux,
		Listen:  frameworks.Listen,
	}
	go func() {
		logging.Printf("server exit: %v", s.ListenAndServe())
	}()
	return s
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	setNoDelay(c.NetConn(), *nodelay)
	c.SetReadDeadline(time.Time{})
}

// setNoDelay sets TCP_NODELAY on an fnet connection. fnet accepts sockets
// itself and sets TCP_NODELAY=1 on them, but its VirtualConn keeps the fd
// unexported and offers no SetNoDelay, so the fd is read through reflection
// (a read of an unexported int field, which reflect allows). An fnet that
// renames or retypes the field stops the server here rather than leaving
// -nodelay=false silently ignored.
func setNoDelay(c net.Conn, nodelay bool) {
	vc, ok := c.(*fnet.VirtualConn)
	if !ok {
		frameworks.SetNoDelay(c, nodelay)
		return
	}
	fd := reflect.ValueOf(vc).Elem().FieldByName("fd")
	if fd.Kind() != reflect.Int {
		logging.Fatalf("fnet.VirtualConn has no int fd field; cannot set TCP_NODELAY")
	}
	if fd.Int() > 0 {
		_ = frameworks.SetNoDelayFD(int(fd.Int()), nodelay)
	}
}
