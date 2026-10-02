package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/taskpool"

	"github.com/gobwas/ws"
	"github.com/linfeip/fnet"
	"github.com/linfeip/fnet/fhttp"
	"github.com/linfeip/fnet/websocket"
)

var (
	nodelay = flag.Bool("nodelay", true, `tcp nodelay`)
	_       = flag.Int("b", 1024, `read buffer size`)
	_       = flag.Int("mrb", 4096, `max read buffer size`)
	_       = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_       = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_       = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)

	// fnet.Options.NumLoops. 0 leaves fnet's own default, max(2, GOMAXPROCS/4).
	// script/server.sh sets it from BENCH_EVENTLOOPS in script/config.sh.
	eventLoops = flag.Int("eventloops", 0, `event loops (NumLoops), 0 for fnet's own default`)
)

// echoHandler echoes every message back.
//
// fnet runs a connection's callbacks one at a time on its executor and hands
// OnMessage a slice of the buffer it just read into, valid for the call.
// WriteMessage has written the frame out by the time it returns, so the echo
// needs no copy; replies written while one read's frames are being handled
// leave in a single write.
type echoHandler struct{}

func (echoHandler) OnOpen(*websocket.Conn) {}

func (echoHandler) OnMessage(c *websocket.Conn, op ws.OpCode, msg []byte) {
	_ = c.WriteMessage(op, msg)
}

func (echoHandler) OnClose(*websocket.Conn, error) {}

func main() {
	flag.Parse()

	// fnet sets TCP_NODELAY on every connection it accepts and exposes no way
	// to undo that, so only the default can be measured; a run that silently
	// ignored -nodelay=false would be reported under the wrong setting.
	if !*nodelay {
		logging.Fatalf("%v cannot run -nodelay=false: it sets TCP_NODELAY on every connection and exposes no way to turn it off", config.Fnet)
	}
	// fnet runs a connection's task - reading, the callbacks, flushing,
	// closing - on an executor and requires that it never run on the caller's
	// stack, which is what the Inline pool would do.
	if taskpool.FlagConfig().Name == taskpool.Inline {
		logging.Fatalf("%v cannot run -taskpool=%v: fnet executors must dispatch asynchronously", config.Fnet, taskpool.Inline)
	}

	// fnet's executor is its own taskpool.DefaultTaskPool. -taskpool hands
	// the work to one of the shared pools instead, so that fnet can be
	// measured on another framework's scheduler; -taskpool=default leaves it
	// on its own.
	engine := fnet.Options{NumLoops: *eventLoops}
	if pool := taskpool.FromFlags(); pool != nil {
		engine.Executor = taskpool.FnetExecutor(pool)
	}

	addrs, err := config.GetFrameworkServerAddrs(config.Fnet)
	if err != nil {
		logging.Fatalf("GetFrameworkServerAddrs(%v) failed: %v", config.Fnet, err)
	}
	control := startControlServer()
	server := startServer(addrs, engine)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	_ = server.Close()
	_ = control.Close()
}

// startServer runs ONE fhttp.Server listening on every address so that all
// ports share a single accept loop and one set of event loops
// (engine.NumLoops), instead of 50 servers x (1 + loops).
//
// fnet opens its listeners itself, so -reuseport has no say in them.
func startServer(addrs []string, engine fnet.Options) *fhttp.Server {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)
	s, err := fhttp.NewServerAddrs(addrs, mux, fhttp.Options{Engine: engine})
	if err != nil {
		logging.Fatalf("fhttp.NewServerAddrs failed: %v", err)
	}
	logging.Printf("%v server: eventloops=%d (0 = fnet's own default)", config.Fnet, engine.NumLoops)
	go func() {
		logging.Printf("server exit: %v", s.Serve())
	}()
	return s
}

// startControlServer serves the routes frameworks.HandleCommon registers -
// /init, /ps, /taskpool and pprof - on the port after the benchmark ones, as
// fib and gws do. They cannot share fnet's port: fhttp refuses a request with
// a body with a 413, and /init is a POST with one. See
// config.frameworkControlPort.
func startControlServer() *http.Server {
	addr, err := config.GetFrameworkHTTPServerAddrs(config.Fnet)
	if err != nil {
		logging.Fatalf("GetFrameworkHTTPServerAddrs(%v) failed: %v", config.Fnet, err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logging.Fatalf("Listen(%v) failed: %v", addr, err)
	}
	mux := &http.ServeMux{}
	frameworks.HandleCommon(mux)
	s := &http.Server{Handler: mux}
	go func() {
		logging.Printf("control server exit: %v", s.Serve(ln))
	}()
	return s
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	// A failed handshake has already been answered with its HTTP error. The
	// connection needs no deadline cleared afterwards: websocket has none
	// unless Options.IdleTimeout asks for one.
	if err := websocket.Upgrade(w, r, echoHandler{}, websocket.Options{}); err != nil {
		log.Printf("upgrade failed: %v", err)
	}
}
