package connection

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"go-websocket-benchmark/config"

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

	// Caculations
	ConnectSuccess uint32
	ConnectFailed  uint32
	BeginTime      time.Time
	EndTime        time.Time

	// All connected connections
	Conns map[*websocket.Conn]struct{}

	Calculator *perf.Calculator

	Engine   *nbhttp.Engine
	Upgrader *websocket.Upgrader
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

	cs.startConnections()
}

func (cs *Connections) CloseConns() {
	for c := range cs.Conns {
		c.Close()
	}
}

func (cs *Connections) init() {
	if cs.DialTimeout <= 0 {
		cs.DialTimeout = time.Second * 1
	}
	if cs.RetryInterval <= 0 {
		cs.RetryInterval = time.Second / 10
	}
	if cs.RetryTimes <= 0 {
		cs.RetryTimes = 3
	}
}

func (cs *Connections) clean() {

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

func (cs *Connections) startConnections() {
	var (
		idx          uint32
		wg           = sync.WaitGroup{}
		done         = make(chan struct{})
		chConnecting = make(chan struct{}, cs.NumConnections)
	)

	cs.startEngine()

	addrs, err := config.GetFrameworkBenchmarkAddrs(cs.Framework, cs.Ip)
	if err != nil {
		log.Fatalf("GetFrameworkBenchmarkAddrs failed: %v", err)
	}

	for i := 0; i < cs.NumConnections; i++ {
		chConnecting <- struct{}{}
	}

	go func() {
		defer func() {
			fmt.Printf("Connections Done: %v Success, %v Failed\n", cs.ConnectSuccess, cs.ConnectFailed)
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

	if cs.RetryTimes <= 0 {
		cs.RetryTimes = 5
	}

	cs.BeginTime = time.Now()

	muxConns := sync.Mutex{}
	for i := 0; i < cs.DialConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
		one:
			for {
				select {
				case <-chConnecting:
				default:
					return
				}

				for i := 0; i < cs.RetryTimes; i++ {
					addr := addrs[atomic.AddUint32(&idx, 1)%uint32(len(addrs))]
					dialer := &websocket.Dialer{
						Engine:      cs.Engine,
						Upgrader:    cs.Upgrader,
						DialTimeout: cs.DialTimeout,
					}
					conn, _, err := dialer.Dial(addr, nil)
					if err == nil {
						conn.SetReadDeadline(time.Time{})
						atomic.AddUint32(&cs.ConnectSuccess, 1)
						muxConns.Lock()
						cs.Conns[conn] = struct{}{}
						muxConns.Unlock()
						goto one
					}
					time.Sleep(cs.RetryInterval)
				}
				atomic.AddUint32(&cs.ConnectFailed, 1)
			}
		}()
	}
	wg.Wait()
	cs.EndTime = time.Now()
	close(done)
}
