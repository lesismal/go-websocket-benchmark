package benchecho

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"runtime"
	"sync"
	"time"

	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/mwsbench/protocol"
	"go-websocket-benchmark/mwsbench/report"

	"github.com/gorilla/websocket"
	"github.com/lesismal/perf"
	"golang.org/x/time/rate"
)

type BenchEcho struct {
	Framework   string
	Ip          string
	WarmupTimes int
	Total       int
	Concurrency int
	Payload     int
	Limit       int
	Percents    []int
	PsInterval  time.Duration

	// OutPreffix string
	// OutSuffix  string

	Calculator *perf.Calculator

	ServerPid int
	PsCounter *perf.PSCounter

	ConnsMap map[*websocket.Conn]struct{}

	pbuffers  [][]byte // payload buffers
	wbuffers  [][]byte // send buffers
	bufferIdx uint32

	chConns chan *websocket.Conn

	limitFn func()

	checkValid bool

	rbufferPool *sync.Pool
}

func New(framework string, serverPid int, benchmarkTimes int, ip string, connsMap map[*websocket.Conn]struct{}, checkValid bool) *BenchEcho {
	bm := &BenchEcho{
		Framework:  framework,
		Ip:         ip,
		Total:      benchmarkTimes,
		ConnsMap:   connsMap,
		limitFn:    func() {},
		checkValid: checkValid,
		ServerPid:  serverPid,
	}
	return bm
}

func (bm *BenchEcho) Run() {
	bm.init()
	defer bm.clean()

	logging.Printf("BenchEcho Warmup for %d times ...", bm.WarmupTimes)
	bm.Calculator.Warmup(bm.Concurrency, bm.WarmupTimes, bm.doOnce)
	logging.Printf("BenchEcho Warmup for %d times done", bm.WarmupTimes)

	// delay 1 second
	chCounterStart := make(chan struct{})
	go func() {
		if bm.PsCounter != nil {
			bm.PsCounter.Start(perf.PSCountOptions{
				CountCPU: true,
				CountMEM: true,
				CountIO:  true,
				CountNET: true,
				Interval: bm.PsInterval,
			})
			time.Sleep(bm.PsInterval)
			close(chCounterStart)
		}
	}()

	logging.Printf("BenchEcho for %d times ...", bm.Total)
	bm.Calculator.Benchmark(bm.Concurrency, bm.Total, bm.doOnce, bm.Percents)
	logging.Printf("BenchEcho for %d times done", bm.Total)

	if bm.PsCounter != nil {
		<-chCounterStart
		bm.PsCounter.Stop()
	}
}

func (bm *BenchEcho) Stop() {

}

func (bm *BenchEcho) Report() report.Report {
	r := &report.BenchEchoReport{
		Framework:   bm.Framework,
		Connections: len(bm.ConnsMap),
		Concurrency: bm.Concurrency,
		Payload:     bm.Payload,
		Total:       bm.Total,
		Success:     bm.Calculator.Success,
		Failed:      bm.Calculator.Failed,
		Used:        int64(bm.Calculator.Used),

		TPS:  bm.Calculator.TPS(),
		Min:  bm.Calculator.Min,
		Avg:  bm.Calculator.Avg,
		Max:  bm.Calculator.Max,
		TP50: bm.Calculator.TPN(50),
		TP75: bm.Calculator.TPN(75),
		TP90: bm.Calculator.TPN(90),
		TP95: bm.Calculator.TPN(95),
		TP99: bm.Calculator.TPN(99),
	}
	if bm.PsCounter != nil {
		// r.GoMin = bm.PsCounter.NumGoroutineMin()
		// r.GoAvg = bm.PsCounter.NumGoroutineAvg()
		// r.GoMax = bm.PsCounter.NumGoroutineMax()
		r.CPUMin = bm.PsCounter.CPUMin()
		r.CPUAvg = bm.PsCounter.CPUAvg()
		r.CPUMax = bm.PsCounter.CPUMax()
		r.MEMRSSMin = bm.PsCounter.MEMRSSMin()
		r.MEMRSSAvg = bm.PsCounter.MEMRSSAvg()
		r.MEMRSSMax = bm.PsCounter.MEMRSSMax()
		r.EER = float64(r.TPS) / r.CPUAvg
	}
	return r
}

func (bm *BenchEcho) init() {
	if bm.WarmupTimes <= 0 {
		bm.WarmupTimes = len(bm.ConnsMap) * 5
		if bm.WarmupTimes > 2000000 {
			bm.WarmupTimes = 2000000
		}
	}
	if bm.Concurrency <= 0 {
		bm.Concurrency = runtime.NumCPU() * 1000
	}
	if bm.Concurrency > len(bm.ConnsMap) {
		bm.Concurrency = len(bm.ConnsMap)
	}
	if bm.Payload <= 0 {
		bm.Payload = 1024
	}
	bm.rbufferPool = &sync.Pool{
		New: func() any {
			buf := make([]byte, bm.Payload)
			return &buf
		},
	}
	if bm.PsInterval <= 0 {
		bm.PsInterval = time.Second
	}
	if len(bm.Percents) == 0 {
		bm.Percents = []int{50, 75, 90, 95, 99}
	}

	if bm.Limit > 0 {
		limiter := rate.NewLimiter(rate.Every(1*time.Second), bm.Limit)
		bm.limitFn = func() {
			limiter.Wait(context.Background())
		}
	}

	bm.pbuffers = make([][]byte, 1024)
	bm.wbuffers = make([][]byte, 1024)
	for i := 0; i < len(bm.pbuffers); i++ {
		buffer := make([]byte, bm.Payload)
		rand.Read(buffer)
		bm.pbuffers[i] = buffer
		bm.wbuffers[i] = protocol.EncodeClientMessage(websocket.BinaryMessage, buffer)
	}

	bm.chConns = make(chan *websocket.Conn, len(bm.ConnsMap))
	for c := range bm.ConnsMap {
		bm.chConns <- c
	}

	psCounter, err := perf.NewPSCounter(bm.ServerPid)
	if err != nil {
		logging.Printf("perf.NewPSCounter failed: %v", err)
	} else {
		bm.PsCounter = psCounter
	}

	bm.Calculator = perf.NewCalculator(fmt.Sprintf("%v-TPS", bm.Framework))
}

func (bm *BenchEcho) clean() {
	bm.chConns = nil
	bm.wbuffers = nil
	bm.bufferIdx = 0
	bm.limitFn = func() {}
}

func (bm *BenchEcho) getBuffers() ([]byte, []byte) {
	idx := uint32(mrand.Intn(len(bm.wbuffers))) % uint32(len(bm.wbuffers))
	return bm.pbuffers[idx], bm.wbuffers[idx]
}

func (bm *BenchEcho) doOnce() error {
	conn := <-bm.chConns
	defer func() {
		bm.chConns <- conn
	}()

	bm.limitFn()

	pbuffer, wbuffer := bm.getBuffers()
	_, err := conn.UnderlyingConn().Write(wbuffer)
	if err != nil {
		return err
	}

	nread := 0
	rbuffer := bm.rbufferPool.Get().(*[]byte)
	defer bm.rbufferPool.Put(rbuffer)
	readBuffer := *rbuffer
	mt, reader, err := conn.NextReader()
	if err != nil {
		return err
	}
	for {
		if nread == len(readBuffer) {
			readBuffer = append(readBuffer, (*rbuffer)...)
		}
		n, err := reader.Read(readBuffer[nread:])
		nread += n
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	if bm.checkValid {
		if mt != websocket.BinaryMessage {
			return errors.New("invalid message type")
		}
		if !bytes.Equal(pbuffer, readBuffer[:nread]) {
			return errors.New("respons data is not equal to origin")
		}
	}

	return nil
}
