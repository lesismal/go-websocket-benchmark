package connection

import (
	"context"
	"fmt"
	"go-websocket-benchmark/config"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lesismal/nbio/logging"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
	"github.com/lesismal/perf"
)

type Connections struct {
	Framework       string
	Ip              string
	DialConcurrency int
	NumConnections  int
	DialTimeout     time.Duration
	RetryInterval   time.Duration
	RetryTimes      int
	Percents        []int

	// Caculations
	ConnectSuccess uint32
	ConnectFailed  uint32

	// All connected connections
	Conns map[*websocket.Conn]struct{}

	Calculator *perf.Calculator

	Engine   *nbhttp.Engine
	Upgrader *websocket.Upgrader

	mux          sync.Mutex
	serverIdx    uint32
	serverAddrs  []string
	chConnecting chan struct{}
}

func New(framework, ip string, dialConcurrency, numConns int) *Connections {
	return &Connections{
		Framework:       framework,
		Ip:              ip,
		NumConnections:  numConns,
		DialConcurrency: dialConcurrency,
		Conns:           map[*websocket.Conn]struct{}{},
	}
}

func (cs *Connections) Run() {
	cs.init()
	defer cs.clean()

	// fmt.Printf("To   Framework  : [%v]", strings.ToUpper(cs.Framework))
	fmt.Printf("New  Connections: [%v]\n", cs.NumConnections)
	fmt.Printf("Dial Concurrency: [%v]\n", cs.DialConcurrency)
	done := make(chan struct{})
	logCone := make(chan struct{})
	go func() {
		defer func() {
			fmt.Printf("Connections done: %v Success, %v Failed\n", cs.ConnectSuccess, cs.ConnectFailed)
			close(logCone)
		}()
		ticker := time.NewTicker(time.Second)
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Printf("%v Connected ...", atomic.LoadUint32(&cs.ConnectSuccess))
			}
		}
	}()

	log.Printf("Connections start ...")
	cs.Calculator.Benchmark(cs.DialConcurrency, cs.NumConnections, cs.doOnce, cs.Percents)

	close(done)
	<-logCone

	// cs.startConnections()
}

func (cs *Connections) Stop() {
	for c := range cs.Conns {
		c.Close()
	}
	cs.Engine.Shutdown(context.Background())
}

func (cs *Connections) init() {
	if cs.DialConcurrency <= 0 {
		cs.DialConcurrency = runtime.NumCPU() * 1024
	}
	if cs.DialTimeout <= 0 {
		cs.DialTimeout = time.Second * 1
	}
	if cs.RetryInterval <= 0 {
		cs.RetryInterval = time.Second / 10
	}
	if cs.RetryTimes <= 0 {
		cs.RetryTimes = 3
	}
	if len(cs.Percents) == 0 {
		cs.Percents = []int{50, 75, 90, 95, 99}
	}

	cs.Calculator = perf.NewCalculator(fmt.Sprintf("%v-Connect", cs.Framework))

	addrs, err := config.GetFrameworkBenchmarkAddrs(cs.Framework, cs.Ip)
	if err != nil {
		log.Fatalf("GetFrameworkBenchmarkAddrs failed: %v", err)
	}
	cs.serverAddrs = addrs

	cs.chConnecting = make(chan struct{}, cs.NumConnections)
	for i := 0; i < cs.NumConnections; i++ {
		cs.chConnecting <- struct{}{}
	}

	cs.startEngine()
}

func (cs *Connections) clean() {
	cs.serverAddrs = nil
	cs.chConnecting = nil
}

func (cs *Connections) startEngine() {
	if cs.Engine != nil {
		return
	}

	logging.SetLevel(logging.LevelError)

	engine := nbhttp.NewEngine(nbhttp.Config{Name: "Benchmark-Client"})
	err := engine.Start()
	if err != nil {
		log.Fatalf("nbhttp.Engine.Start failed: %v\n", err)
	}
	cs.Engine = engine

	upgrader := websocket.NewUpgrader()
	upgrader.Engine = engine
	upgrader.OnMessage(func(c *websocket.Conn, mt websocket.MessageType, b []byte) {})
	cs.Upgrader = upgrader

	time.Sleep(time.Second)
}

// func (cs *Connections) startConnections() {
// 	done := make(chan struct{})
// 	logCone := make(chan struct{})
// 	go func() {
// 		defer func() {
// 			fmt.Printf("Connections done: %v Success, %v Failed\n", cs.ConnectSuccess, cs.ConnectFailed)
// 			close(logCone)
// 		}()
// 		ticker := time.NewTicker(time.Second)
// 		for {
// 			select {
// 			case <-done:
// 				return
// 			case <-ticker.C:
// 				fmt.Printf("%v Connected ...", atomic.LoadUint32(&cs.ConnectSuccess))
// 			}
// 		}
// 	}()

// 	log.Printf("Connections start ...")
// 	cs.Calculator.Benchmark(cs.DialConcurrency, cs.NumConnections, cs.doOnce, cs.Percents)

// 	close(done)
// 	<-logCone
// }

func (cs *Connections) doOnce() error {
begin:
	for {
		select {
		case <-cs.chConnecting:
		default:
			return nil
		}

		for i := 0; i < cs.RetryTimes; i++ {
			addr := cs.serverAddrs[atomic.AddUint32(&cs.serverIdx, 1)%uint32(len(cs.serverAddrs))]
			dialer := &websocket.Dialer{
				Engine:      cs.Engine,
				Upgrader:    cs.Upgrader,
				DialTimeout: cs.DialTimeout,
			}
			conn, _, err := dialer.Dial(addr, nil)
			if err == nil {
				conn.SetReadDeadline(time.Time{})
				atomic.AddUint32(&cs.ConnectSuccess, 1)
				cs.mux.Lock()
				cs.Conns[conn] = struct{}{}
				cs.mux.Unlock()
				goto begin
			}
			time.Sleep(cs.RetryInterval)
		}
		atomic.AddUint32(&cs.ConnectFailed, 1)
	}
}
