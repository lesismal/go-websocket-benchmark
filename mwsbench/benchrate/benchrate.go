package benchrate

import (
	"bytes"
	"context"
	"crypto/rand"
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
	Framework       string
	Ip              string
	Duration        time.Duration
	ConnConcurrency int
	Payload         int
	SendLimit       int
	PsInterval      time.Duration

	ServerPid int
	PsCounter *perf.PSCounter

	ConnsMap map[*websocket.Conn]struct{}

	wbuffer []byte

	chConns chan *websocket.Conn

	limitFn func()

	sendTimes int64
	sendBytes int64
	recvTimes int64
	recvBytes int64
}

func New(framework string, ip string, connsMap map[*websocket.Conn]struct{}) *BenchRate {
	bm := &BenchRate{
		Framework: framework,
		Ip:        ip,
		ConnsMap:  connsMap,
		limitFn:   func() {},
	}
	return bm
}

func (br *BenchRate) Run() {
	br.init()
	defer br.clean()

	chCounterStart := make(chan struct{})
	go func() {
		br.PsCounter.Start(perf.PSCountOptions{
			CountCPU: true,
			CountMEM: true,
			CountIO:  true,
			CountNET: true,
			Interval: br.PsInterval,
		})
		time.Sleep(br.PsInterval)
		close(chCounterStart)
	}()

	done := make(chan struct{})
	time.AfterFunc(br.Duration, func() {
		close(done)
	})

	logging.Printf("BenchRate for %.2f seconds ...", br.Duration.Seconds())

	wg := sync.WaitGroup{}
	for i := 0; i < br.ConnConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
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
	wg.Wait()

	logging.Printf("BenchRate for %.2f seconds done", br.Duration.Seconds())

	<-chCounterStart
	br.PsCounter.Stop()
}

func (br *BenchRate) Stop() {

}

func (br *BenchRate) Report() report.Report {
	return &report.BenchRateReport{
		Framework:       br.Framework,
		Duration:        br.Duration.Nanoseconds(),
		Connections:     len(br.ConnsMap),
		ConnConcurrency: br.ConnConcurrency,
		Payload:         br.Payload,
		SendTimes:       br.sendTimes,
		SendBytes:       br.sendBytes,
		RecvTimes:       br.recvTimes,
		RecvBytes:       br.recvBytes,
		CPUMin:          br.PsCounter.CPUMin(),
		CPUAvg:          br.PsCounter.CPUAvg(),
		CPUMax:          br.PsCounter.CPUMax(),
		MEMRSSMin:       br.PsCounter.MEMRSSMin(),
		MEMRSSAvg:       br.PsCounter.MEMRSSAvg(),
		MEMRSSMax:       br.PsCounter.MEMRSSMax(),
	}
}

func (br *BenchRate) init() {
	if br.Duration <= 0 {
		br.Duration = time.Second * 10
	}
	if br.ConnConcurrency <= 0 {
		br.ConnConcurrency = runtime.NumCPU() * 1000
	}
	if br.ConnConcurrency > len(br.ConnsMap) {
		br.ConnConcurrency = len(br.ConnsMap)
	}
	if br.Payload <= 0 {
		br.Payload = 1024
	}
	if br.PsInterval <= 0 {
		br.PsInterval = time.Second
	}

	if br.SendLimit > 0 {
		limiter := rate.NewLimiter(rate.Every(1*time.Second), br.SendLimit)
		br.limitFn = func() {
			limiter.Wait(context.Background())
		}
	}

	br.wbuffer = make([]byte, br.Payload)
	rand.Read(br.wbuffer)

	br.chConns = make(chan *websocket.Conn, len(br.ConnsMap)*br.ConnConcurrency)
	for c := range br.ConnsMap {
		c.OnMessage(br.onMessage)
	}
	for i := 0; i < br.ConnConcurrency; i++ {
		for c := range br.ConnsMap {
			br.chConns <- c
		}
	}

	serverPid, err := config.GetFrameworkPid(br.Framework, br.Ip)
	if err != nil {
		logging.Fatalf("BenchRate GetFrameworkPid(%v) failed: %v", br.Framework, err)
	}
	br.ServerPid = serverPid
	psCounter, err := perf.NewPSCounter(serverPid)
	if err != nil {
		panic(err)
	}
	br.PsCounter = psCounter
}

func (br *BenchRate) clean() {
	br.chConns = nil
	br.limitFn = func() {}
}

func (br *BenchRate) getWriteBuffer() []byte {
	return br.wbuffer
}

func (br *BenchRate) doOnce() error {
	conn := <-br.chConns
	defer func() {
		br.chConns <- conn
	}()

	br.limitFn()

	err := conn.WriteMessage(websocket.BinaryMessage, br.getWriteBuffer())
	if err == nil {
		atomic.AddInt64(&br.sendTimes, 1)
		atomic.AddInt64(&br.sendBytes, int64(br.Payload))
	}

	return err
}

func (br *BenchRate) onMessage(c *websocket.Conn, mt websocket.MessageType, b []byte) {
	if mt == websocket.BinaryMessage && bytes.Equal(b, br.getWriteBuffer()) {
		atomic.AddInt64(&br.recvTimes, 1)
		atomic.AddInt64(&br.recvBytes, int64(len(b)))
	}
}
