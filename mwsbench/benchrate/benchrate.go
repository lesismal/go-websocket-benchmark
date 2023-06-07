package benchrate

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/mwsbench/report"

	"github.com/lesismal/nbio/nbhttp/websocket"
	"github.com/lesismal/perf"
	"golang.org/x/time/rate"
)

type BenchRate struct {
	Framework   string
	Ip          string
	WarmupTimes int
	Total       int
	Concurrency int
	Payload     int
	Limit       int
	Percents    []int
	PsInterval  time.Duration

	OutPreffix string
	OutSuffix  string

	Calculator *perf.Calculator
	PsCounter  *perf.PSCounter

	ConnsMap map[*websocket.Conn]struct{}

	wbuffers  [][]byte
	bufferIdx uint32

	chConns chan *websocket.Conn

	limitFn func()

	rbufferPool *sync.Pool

	sendTimes int64
	sendBytes int64
	recvTimes int64
	recvBytes int64
}

func New(framework string, benchmarkTimes int, ip string, connsMap map[*websocket.Conn]struct{}) *BenchRate {
	bm := &BenchRate{
		Framework: framework,
		Ip:        ip,
		Total:     benchmarkTimes,
		ConnsMap:  connsMap,
		limitFn:   func() {},
	}
	return bm
}

func (br *BenchRate) Run() {
	br.init()
	defer br.clean()

	// logging.Printf("Warmup for %d times ...", br.WarmupTimes)
	// br.Calculator.Warmup(br.Concurrency, br.WarmupTimes, br.doOnce)
	// logging.Printf("Warmup for %d times done", br.WarmupTimes)

	// delay 1 second
	chCounterStart := make(chan struct{})
	time.AfterFunc(time.Second, func() {
		br.PsCounter.Start(perf.PSCountOptions{
			CountCPU: true,
			CountMEM: true,
			CountIO:  true,
			CountNET: true,
			Interval: br.PsInterval,
		})
		time.Sleep(br.PsInterval)
		close(chCounterStart)
	})

	done := make(chan struct{})
	time.AfterFunc(time.Second*10, func() {
		close(done)
	})
	for i := 0; i < br.Concurrency; i++ {
		// for i := 0; i < 1; i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					br.doOnce()
				}
			}
		}()
	}
	<-done
	// logging.Printf("Benchmark for %d times ...", br.Total)
	// br.Calculator.Benchmark(br.Concurrency, math.MaxInt, br.doOnce, br.Percents)
	// logging.Printf("Benchmark for %d times done", br.Total)

	<-chCounterStart
	br.PsCounter.Stop()
}

func (br *BenchRate) Stop() {

}

func (br *BenchRate) Report() report.Report {
	return &report.BenchRateReport{
		Framework:   br.Framework,
		Connections: len(br.ConnsMap),
		Concurrency: br.Concurrency,
		Payload:     br.Payload,
		Total:       br.Total,
		Success:     br.Calculator.Success,
		Failed:      br.Calculator.Failed,
		Used:        int64(br.Calculator.Used),
		CPUMin:      br.PsCounter.CPUMin(),
		CPUAvg:      br.PsCounter.CPUAvg(),
		CPUMax:      br.PsCounter.CPUMax(),
		MEMRSSMin:   br.PsCounter.MEMRSSMin(),
		MEMRSSAvg:   br.PsCounter.MEMRSSAvg(),
		MEMRSSMax:   br.PsCounter.MEMRSSMax(),
		SendTimes:   br.sendTimes,
		SendBytes:   br.sendBytes,
		RecvTimes:   br.recvTimes,
		RecvBytes:   br.recvBytes,
	}
}

func (br *BenchRate) init() {
	if br.WarmupTimes <= 0 {
		br.WarmupTimes = len(br.ConnsMap) * 5
		if br.WarmupTimes > 2000000 {
			br.WarmupTimes = 2000000
		}
	}
	if br.Concurrency <= 0 {
		br.Concurrency = runtime.NumCPU() * 1000
	}
	if br.Concurrency > len(br.ConnsMap) {
		br.Concurrency = len(br.ConnsMap)
	}
	if br.Payload <= 0 {
		br.Payload = 1024
	}
	br.rbufferPool = &sync.Pool{
		New: func() any {
			buf := make([]byte, br.Payload)
			return &buf
		},
	}
	if br.PsInterval <= 0 {
		br.PsInterval = time.Second
	}
	if len(br.Percents) == 0 {
		br.Percents = []int{50, 75, 90, 95, 99}
	}

	// if br.Limit <= 0 {
	// 	br.Limit = 500000
	// }
	if br.Limit > 0 {
		limiter := rate.NewLimiter(rate.Every(1*time.Second), br.Limit)
		br.limitFn = func() {
			limiter.Wait(context.Background())
		}
	}

	br.wbuffers = make([][]byte, 1024)
	for i := 0; i < len(br.wbuffers); i++ {
		buffer := make([]byte, br.Payload)
		rand.Read(buffer)
		br.wbuffers[i] = buffer
	}

	window := 3
	br.chConns = make(chan *websocket.Conn, len(br.ConnsMap)*window)
	for c := range br.ConnsMap {
		c.OnMessage(br.onMessage)
	}
	for i := 0; i < window; i++ {
		for c := range br.ConnsMap {
			br.chConns <- c
		}
	}

	serverPid, err := config.GetFrameworkPid(br.Framework, br.Ip)
	if err != nil {
		logging.Fatalf("BenchRate   GetFrameworkPid(%v) failed: %v", br.Framework, err)
	}
	psCounter, err := perf.NewPSCounter(serverPid)
	if err != nil {
		panic(err)
	}
	br.PsCounter = psCounter

	br.Calculator = perf.NewCalculator(fmt.Sprintf("%v-TPS", br.Framework))
}

func (br *BenchRate) clean() {
	br.chConns = nil
	// br.wbuffers = nil
	br.bufferIdx = 0
	br.limitFn = func() {}
}

func (br *BenchRate) getWriteBuffer() []byte {
	return br.wbuffers[atomic.AddUint32(&br.bufferIdx, 1)%uint32(len(br.wbuffers))]
}

func (br *BenchRate) doOnce() error {
	conn := <-br.chConns

	br.limitFn()

	err := conn.WriteMessage(websocket.BinaryMessage, br.getWriteBuffer())
	if err == nil {
		atomic.AddInt64(&br.sendTimes, 1)
		atomic.AddInt64(&br.sendBytes, int64(br.Payload))
	}
	// if err != nil {
	// 	panic(err)
	// }
	return err
}

func (br *BenchRate) onMessage(c *websocket.Conn, mt websocket.MessageType, b []byte) {
	atomic.AddInt64(&br.recvTimes, 1)
	atomic.AddInt64(&br.recvBytes, int64(len(b)))
	br.chConns <- c
}
