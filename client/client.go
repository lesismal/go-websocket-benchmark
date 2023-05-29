package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
	"github.com/lesismal/perf"
	"golang.org/x/time/rate"
)

const bufferNum uint32 = 1000

var (
	ip             = flag.String("ip", "127.0.0.1", `ip, e.g. "127.0.0.1"`)
	portRange      = flag.String("ports", "18001:18050", `port range, e.g. "18001:18050"`)
	numClient      = flag.Int("c", 300000, "client num")
	numGoroutine   = flag.Int("g", 2000, "goroutine num")
	payloadSize    = flag.Int("b", 1024, `payload size`)
	benchmarkTimes = flag.Int("n", 1000000, `benchmark times`)
	maxTPS         = flag.Int("l", 0, `max benchmark tps`)

	buffers   [][]byte
	bufferIdx uint32

	chConns chan *websocket.Conn
)

func main() {
	flag.Parse()

	startClients()

	initBuffers()

	startBenchmark()
}

type EchoResult struct {
	mt websocket.MessageType
	b  []byte
}

func startClients() {
	var connected uint32
	var done = make(chan struct{})
	var ticker = time.NewTicker(time.Second)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				log.Printf("%v clients connected", connected)
			}
		}
	}()

	chConns = make(chan *websocket.Conn, *numClient)

	engine := nbhttp.NewEngine(nbhttp.Config{})
	err := engine.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}
	upgrader := websocket.NewUpgrader()
	upgrader.Engine = engine
	upgrader.OnMessage(func(c *websocket.Conn, mt websocket.MessageType, b []byte) {
		ch, _ := c.Session().(chan EchoResult)
		ch <- EchoResult{
			mt: mt,
			b:  b,
		}
	})

	time.Sleep(time.Second / 10)

	log.Printf("%v clients start connecting", *numClient)
	defer log.Printf("%v clients connected", *numClient)

	ports := strings.Split(*portRange, ":")
	minPort, err := strconv.Atoi(ports[0])
	if err != nil {
		log.Fatalf("invalid port range: %v, %v", *portRange, err)
	}
	maxPort, err := strconv.Atoi(ports[1])
	if err != nil {
		log.Fatalf("invalid port range: %v, %v", *portRange, err)
	}
	addrs := []string{}
	for i := minPort; i <= maxPort; i++ {
		addrs = append(addrs, fmt.Sprintf("ws://%v:%d/ws", *ip, i))
	}

	wg := sync.WaitGroup{}
	var addrIdx uint32
	for i := 0; i < *numGoroutine; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < (*numClient)/(*numGoroutine); j++ {
				var addr = addrs[atomic.AddUint32(&addrIdx, 1)%uint32(len(addrs))]
				var err error
				var conn *websocket.Conn
				var dialer = &websocket.Dialer{
					Engine:      engine,
					Upgrader:    upgrader,
					DialTimeout: time.Second * 5,
				}
				for k := 0; k < 5; k++ {
					conn, _, err = dialer.Dial(addr, nil)
					if err == nil {
						conn.SetSession(make(chan EchoResult, 1))
						atomic.AddUint32(&connected, 1)
						chConns <- conn
						break
					}
					time.Sleep(time.Second / 10)
				}
				if err != nil {
					log.Fatalf("connect failed: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	close(done)
	time.Sleep(time.Second / 10)
}

func initBuffers() {
	buffers = make([][]byte, bufferNum)
	for i := uint32(0); i < bufferNum; i++ {
		buffer := make([]byte, *payloadSize)
		rand.Read(buffer)
		buffers[i] = buffer
	}
}

func getBuffer() []byte {
	return buffers[atomic.AddUint32(&bufferIdx, 1)%bufferNum]
}

func startBenchmark() {
	var limiter *rate.Limiter
	if *maxTPS > 0 {
		limiter = rate.NewLimiter(rate.Every(1*time.Second), *maxTPS)
	}

	oneTask := func() error {
		if limiter != nil {
			limiter.Wait(context.Background())
		}
		conn := <-chConns
		buffer := getBuffer()
		err := conn.WriteMessage(websocket.BinaryMessage, buffer)
		if err != nil {
			log.Fatalf("write failed: %v", err)
		}
		ch := conn.Session().(chan EchoResult)
		ret := <-ch
		if ret.mt != websocket.BinaryMessage {
			log.Fatalf("invalid message type: %v", ret.mt)
		}
		if !bytes.Equal(buffer, ret.b) {
			log.Fatalf("response not equal to request")
		}
		chConns <- conn
		return nil
	}
	tpPercents := []int{50, 60, 70, 80, 90, 95, 99, 999}

	psCounter, err := perf.NewPSCounter(0)
	if err != nil {
		panic(err)
	}

	calculator := perf.NewCalculator("test")
	calculator.Warmup(*numGoroutine, *benchmarkTimes/10, oneTask)

	psCounter.Start(perf.PSCountOptions{
		CountCPU: true,
		CountMEM: true,
		CountIO:  true,
		CountNET: true,
		Interval: time.Second,
	})
	calculator.Benchmark(*numGoroutine, *benchmarkTimes, oneTask, tpPercents)
	psCounter.Stop()

	fmt.Println("-------------------------")
	fmt.Println(calculator.String())
	fmt.Printf("TP50: %.2fms\n", float64(calculator.TPN(50))/1000000.0)
	fmt.Println("-------------------------")
	// fmt.Println(psCounter.Json())
	fmt.Println("-------------------------")
	fmt.Println("CPUMin:", psCounter.CPUMin())
	fmt.Println("CPUMax:", psCounter.CPUMax())
	fmt.Println("CPUAvg:", psCounter.CPUAvg())
	fmt.Println("-------------------------")
	fmt.Println("MEMRSSMin:", psCounter.MEMRSSMin())
	fmt.Println("MEMRSSMax :", psCounter.MEMRSSMax())
	fmt.Println("MEMRSSAvg :", psCounter.MEMRSSAvg())
	fmt.Println("-------------------------")
	fmt.Println("MEMVMSMin:", psCounter.MEMVMSMin())
	fmt.Println("MEMVMSMax :", psCounter.MEMVMSMax())
	fmt.Println("MEMVMSAvg :", psCounter.MEMVMSAvg())
	fmt.Println("-------------------------")
}
