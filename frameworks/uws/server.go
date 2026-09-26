package main

import (
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/taskpool"

	"github.com/urpc/uio"
	"github.com/urpc/uio/uws"
)

var (
	nodelay        = flag.Bool("nodelay", true, `tcp nodelay`)
	readBufferSize = flag.Int("b", 1024, `read buffer size`)
	_              = flag.Int("mrb", 4096, `max read buffer size`)
	_              = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_              = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
	_              = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)
)

type echoHandler struct{}

func (echoHandler) OnOpen(conn *uws.Conn) {
	if !*nodelay {
		conn.SetNoDelay(false)
	}
}

func (echoHandler) OnMessage(conn *uws.Conn, message uws.Message) {
	switch message.Type {
	case uws.TextMessage:
		_ = conn.SendText(message.Payload)
	case uws.BinaryMessage:
		_ = conn.SendBinary(message.Payload)
	}
}

func (echoHandler) OnClose(*uws.Conn, uws.CloseEvent) {}

func main() {
	flag.Parse()

	if *readBufferSize <= 0 {
		logging.Fatalf("read buffer size must be positive: %d", *readBufferSize)
	}
	// UIO runs every connection's callbacks in a task off its event loops, and
	// a UIO executor must not run that task inline.
	if supportsExecutor && taskpool.FlagConfig().Name == taskpool.Inline {
		logging.Fatalf("%v cannot run -taskpool=%v: UIO executors must dispatch asynchronously", frameworkName, taskpool.Inline)
	}
	addrs, err := config.GetFrameworkServerAddrs(frameworkName)
	if err != nil {
		logging.Fatalf("GetFrameworkServerAddrs(%v) failed: %v", frameworkName, err)
	}
	if len(addrs) == 0 {
		logging.Fatalf("no websocket listen addresses configured")
	}

	server := uws.NewServer(echoHandler{})
	server.Events = &uio.Events{
		Pollers:       runtime.NumCPU(),
		MaxBufferSize: maxBufferSize,
	}
	// UIO runs each connection's I/O as one task. An explicit benchmark pool
	// runs those tasks in place of UIO's own scheduler; -taskpool=default keeps
	// UIO's taskgo scheduler, which runs about one worker per P and adds
	// workers, up to 512*GOMAXPROCS, while callbacks block. The stdio backend
	// reads and writes on goroutines of its own and takes no executor.
	if supportsExecutor {
		if pool := taskpool.FromFlags(); pool != nil {
			server.Events.Executor = taskpool.UwsExecutor{Pool: pool}
		}
	}
	logging.Printf(
		"uws benchmark config: pollers=%d GOMAXPROCS=%d NumCPU=%d",
		server.Events.Pollers, runtime.GOMAXPROCS(0), runtime.NumCPU(),
	)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(addrs...) }()

	pidLn := startHTTPServer()
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	var serveErr error
	select {
	case serveErr = <-serveDone:
	case <-interrupt:
		_ = server.Close(nil)
		serveErr = <-serveDone
	}
	signal.Stop(interrupt)
	_ = pidLn.Close()
	logging.Printf("server exit: %v", serveErr)
}

func startHTTPServer() net.Listener {
	addr, err := config.GetFrameworkHTTPServerAddrs(frameworkName)
	if err != nil {
		logging.Fatalf("GetFrameworkHTTPServerAddrs(%v) failed: %v", frameworkName, err)
	}
	mux := &http.ServeMux{}
	frameworks.HandleCommon(mux)
	ln, err := frameworks.Listen("tcp", addr)
	if err != nil {
		logging.Fatalf("Listen(%v) failed: %v", addr, err)
	}
	go func() {
		logging.Printf("pid server exit: %v", http.Serve(ln, mux))
	}()
	return ln
}
