package main

import (
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync/atomic"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/frameworks"
	"go-websocket-benchmark/logging"

	"github.com/urpc/uio"
	"github.com/urpc/uio/uws"
)

const (
	executorWorkers = 256
	executorPending = 65536
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

type benchmarkExecutor struct {
	shards []chan func()
	next   atomic.Uint64
}

func newBenchmarkExecutor() *benchmarkExecutor {
	shardCount := min(runtime.GOMAXPROCS(0), executorWorkers/8, executorPending)
	executor := &benchmarkExecutor{shards: make([]chan func(), shardCount)}
	for index := range executor.shards {
		queueSize := executorPending / shardCount
		if index < executorPending%shardCount {
			queueSize++
		}
		executor.shards[index] = make(chan func(), queueSize)
		workerCount := executorWorkers / shardCount
		if index < executorWorkers%shardCount {
			workerCount++
		}
		for range workerCount {
			queue := executor.shards[index]
			go func() {
				for task := range queue {
					task()
				}
			}()
		}
	}
	return executor
}

func (executor *benchmarkExecutor) Submit(task func()) bool {
	index := (executor.next.Add(1) - 1) % uint64(len(executor.shards))
	select {
	case executor.shards[index] <- task:
		return true
	default:
		return false
	}
}

func main() {
	flag.Parse()

	if *readBufferSize <= 0 {
		logging.Fatalf("read buffer size must be positive: %d", *readBufferSize)
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
	server.Executor = newBenchmarkExecutor()
	logging.Printf(
		"uws benchmark config: executor=sharded workers=%d pending=%d pollers=%d GOMAXPROCS=%d NumCPU=%d",
		executorWorkers, executorPending,
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
