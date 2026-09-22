package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
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

	upgrader = &websocket.Upgrader{OnOpen: onOpen, OnMessage: onMessage, OnClose: onClose}

	// pool is nil only under -taskpool=default, which keeps the run on
	// fnet's own arrangement: the echo happens on the reactor goroutine
	// that parsed the frame.
	pool taskpool.Pool

	// echoBuffers holds the copies the pool echoes; see onMessage.
	echoBuffers = sync.Pool{New: func() any { buffer := make([]byte, 0, 1024); return &buffer }}
)

// streams gives each connection its own ordered feed into the pool, so that
// a connection's replies cannot overtake each other on the way out.
var streams sync.Map // *websocket.Conn -> *taskpool.Serial

func onOpen(c *websocket.Conn) {
	if pool != nil {
		streams.Store(c, taskpool.NewSerial(pool))
	}
}

func onClose(c *websocket.Conn, _ error) { streams.Delete(c) }

// onMessage echoes the frame back.
//
// fnet has no pool of its own to hand a shared one to: it parses frames on
// its reactor goroutine and calls OnMessage inline, and the payload is a
// view into that reactor's read buffer which is only valid for the call. So
// the pool echoes a copy, and the copy is the only thing the pooled run pays
// that the default run does not.
//
// A pool that refuses the work echoes inline instead, since a dropped echo
// would leave the benchmark client waiting on a reply that never comes.
func onMessage(c *websocket.Conn, op websocket.OpCode, msg []byte) {
	stream, ok := streams.Load(c)
	if !ok {
		_ = c.WriteMessage(op, msg)
		return
	}
	serial := stream.(*taskpool.Serial)

	buffer := echoBuffers.Get().(*[]byte)
	*buffer = append((*buffer)[:0], msg...)
	echo := func() {
		_ = c.WriteMessage(op, *buffer)
		echoBuffers.Put(buffer)
	}
	if serial.Go(echo) {
		return
	}
	for _, queued := range serial.Take() {
		queued()
	}
}

func main() {
	flag.Parse()

	pool = taskpool.FromFlags()

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
	frameworks.SetNoDelay(c.NetConn(), *nodelay)
	c.SetReadDeadline(time.Time{})
}
