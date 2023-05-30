package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go-websocket-benchmark/conf"

	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
	"github.com/lesismal/perf"
	"golang.org/x/time/rate"
)

const bufferNum uint32 = 1000

var (
	ip             = flag.String("ip", "127.0.0.1", `ip, e.g. "127.0.0.1"`)
	framework      = flag.String("f", conf.NbioBasedonStdhttp, `framework, e.g. "gorilla"`)
	numClient      = flag.Int("c", 300000, "client num")
	numGoroutine   = flag.Int("g", 2000, "goroutine num")
	payloadSize    = flag.Int("b", 1024, `payload size`)
	benchmarkTimes = flag.Int("n", 1000000, `benchmark times`)
	maxTPS         = flag.Int("l", 0, `max benchmark tps`)
	memLimit       = flag.Int64("m", 1024*1024*1024*4, `memory limit`)

	buffers   [][]byte
	bufferIdx uint32

	chConns chan *websocket.Conn
)

func main() {
	flag.Parse()

	debug.SetMemoryLimit(*memLimit)

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

	engine := nbhttp.NewEngine(nbhttp.Config{Name: "Benchmark-Client"})
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

	portRange := conf.Ports[*framework]
	ports := strings.Split(conf.Ports[*framework], ":")
	minPort, err := strconv.Atoi(ports[0])
	if err != nil {
		log.Fatalf("invalid port range: %v, %v", portRange, err)
	}
	maxPort, err := strconv.Atoi(ports[1])
	if err != nil {
		log.Fatalf("invalid port range: %v, %v", portRange, err)
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
		defer mempool.Free(ret.b)
		if ret.mt != websocket.BinaryMessage {
			log.Fatalf("invalid message type: %v", ret.mt)
		}
		if !bytes.Equal(buffer, ret.b) {
			log.Fatalf("response not equal to request")
		}
		chConns <- conn
		return nil
	}
	tpPercents := []int{50, 75, 90, 95, 99}

	procName := *framework + ".server"
	psCounter, err := perf.NewPSCounterByProcName(procName)
	if err != nil {
		panic(err)
	}

	calculator := perf.NewCalculator(*framework)
	calculator.Warmup(*numGoroutine, *benchmarkTimes/10, oneTask)
	psCounter.Start(perf.PSCountOptions{
		CountCPU: true,
		CountMEM: true,
		CountIO:  true,
		CountNET: true,
		Interval: time.Second,
	})
	calculator.Benchmark(*numGoroutine, *benchmarkTimes, oneTask, tpPercents)
	calculator.TPS()
	psCounter.Stop()
	fmt.Println("-------------------------")
	fmt.Println(calculator.String())
	fmt.Printf(`CPU MIN  : %.2f%%,
CPU MIN  : %.2f%%,
CPU MIN  : %.2f%%,
MEM MIN  : %v,
MEM MIN  : %v,
MEM MIN  : %v
`,
		psCounter.CPUMin(),
		psCounter.CPUAvg(),
		psCounter.CPUMax(),
		psCounter.MEMRSSMin(),
		psCounter.MEMRSSAvg(),
		psCounter.MEMRSSMax())

	report := &Report{
		Total:     int64(calculator.Total),
		Success:   calculator.Success,
		Failed:    calculator.Failed,
		TimeUsed:  calculator.Used,
		TPS:       calculator.TPS(),
		TP50:      calculator.TPN(50),
		TP75:      calculator.TPN(75),
		TP90:      calculator.TPN(90),
		TP95:      calculator.TPN(95),
		TP99:      calculator.TPN(99),
		CPUMin:    psCounter.CPUMin(),
		CPUAvg:    psCounter.CPUAvg(),
		CPUMax:    psCounter.CPUMax(),
		MEMRSSMin: psCounter.MEMRSSMin(),
		MEMRSSAvg: psCounter.MEMRSSAvg(),
		MEMRSSMax: psCounter.MEMRSSMax(),
	}
	b, err := json.Marshal(report)
	if err != nil {
		log.Fatalf("Marshal Report failed: %v", err)
	}
	err = ioutil.WriteFile("./output/report/"+*framework+" .json", b, 0666)
	if err != nil {
		log.Fatalf("Write Report failed: %v", err)
	}
	fmt.Println("-------------------------")
}

type Report struct {
	Total     int64
	Success   int64
	Failed    int64
	TimeUsed  time.Duration
	TPS       int64
	TP50      int64
	TP75      int64
	TP90      int64
	TP95      int64
	TP99      int64
	CPUMin    float64
	CPUAvg    float64
	CPUMax    float64
	MEMRSSMin uint64
	MEMRSSAvg uint64
	MEMRSSMax uint64
}
