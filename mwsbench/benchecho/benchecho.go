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

	onWarmup     func()
	onBenchmark  func()
	pprofDataCPU []byte `json:"-" md:"-"`
	pprofDataMEM []byte `json:"-" md:"-"`
}

func New(framework string, serverPid int, benchmarkTimes int, ip string, connsMap map[*websocket.Conn]struct{}, checkValid bool) *BenchEcho {
	be := &BenchEcho{
		Framework:  framework,
		Ip:         ip,
		Total:      benchmarkTimes,
		ConnsMap:   connsMap,
		limitFn:    func() {},
		checkValid: checkValid,
		ServerPid:  serverPid,
	}
	return be
}

func (be *BenchEcho) Run() {
	be.init()
	defer be.clean()

	logging.Printf("BenchEcho Warmup for %d times ...", be.WarmupTimes)
	if be.onWarmup != nil {
		be.onWarmup()
	}
	be.Calculator.Warmup(be.Concurrency, be.WarmupTimes, be.doOnce)
	logging.Printf("BenchEcho Warmup for %d times done", be.WarmupTimes)

	// delay 1 second
	chCounterStart := make(chan struct{})
	go func() {
		if be.PsCounter != nil {
			be.PsCounter.Start(perf.PSCountOptions{
				CountCPU: true,
				CountMEM: true,
				CountIO:  true,
				CountNET: true,
				Interval: be.PsInterval,
			})
			time.Sleep(be.PsInterval)
			close(chCounterStart)
		}
	}()

	logging.Printf("BenchEcho for %d times ...", be.Total)
	if be.onBenchmark != nil {
		be.onBenchmark()
	}
	be.Calculator.Benchmark(be.Concurrency, be.Total, be.doOnce, be.Percents)
	logging.Printf("BenchEcho for %d times done", be.Total)

	if be.PsCounter != nil {
		<-chCounterStart
		be.PsCounter.Stop()
	}
}

func (be *BenchEcho) Stop() {

}

func (be *BenchEcho) OnWarmup(f func()) {
	be.onWarmup = f
}

func (be *BenchEcho) OnBenchmark(f func()) {
	be.onBenchmark = f
}

func (be *BenchEcho) SetPprofData(cpu, mem []byte) {
	be.pprofDataCPU = cpu
	be.pprofDataMEM = mem
}

func (be *BenchEcho) Report() *report.BenchEchoReport {
	r := &report.BenchEchoReport{
		Framework:   be.Framework,
		Connections: len(be.ConnsMap),
		Concurrency: be.Concurrency,
		Payload:     be.Payload,
		Total:       be.Total,
		Success:     be.Calculator.Success,
		Failed:      be.Calculator.Failed,
		Used:        int64(be.Calculator.Used),

		TPS:  be.Calculator.TPS(),
		Min:  be.Calculator.Min,
		Avg:  be.Calculator.Avg,
		Max:  be.Calculator.Max,
		TP50: be.Calculator.TPN(50),
		TP75: be.Calculator.TPN(75),
		TP90: be.Calculator.TPN(90),
		TP95: be.Calculator.TPN(95),
		TP99: be.Calculator.TPN(99),
	}
	r.SetPprofData(be.pprofDataCPU, be.pprofDataMEM)

	if be.PsCounter != nil {
		// r.GoMin = be .PsCounter.NumGoroutineMin()
		// r.GoAvg = be .PsCounter.NumGoroutineAvg()
		// r.GoMax = be .PsCounter.NumGoroutineMax()
		r.CPUMin = be.PsCounter.CPUMin()
		r.CPUAvg = be.PsCounter.CPUAvg()
		r.CPUMax = be.PsCounter.CPUMax()
		r.MEMRSSMin = be.PsCounter.MEMRSSMin()
		r.MEMRSSAvg = be.PsCounter.MEMRSSAvg()
		r.MEMRSSMax = be.PsCounter.MEMRSSMax()
		r.EER = float64(r.TPS) / r.CPUAvg
	}
	return r
}

func (be *BenchEcho) init() {
	if be.WarmupTimes <= 0 {
		be.WarmupTimes = len(be.ConnsMap) * 5
		if be.WarmupTimes > 2000000 {
			be.WarmupTimes = 2000000
		}
	}
	if be.Concurrency <= 0 {
		be.Concurrency = runtime.NumCPU() * 1000
	}
	if be.Concurrency > len(be.ConnsMap) {
		be.Concurrency = len(be.ConnsMap)
	}
	if be.Payload <= 0 {
		be.Payload = 1024
	}
	be.rbufferPool = &sync.Pool{
		New: func() any {
			buf := make([]byte, be.Payload)
			return &buf
		},
	}
	if be.PsInterval <= 0 {
		be.PsInterval = time.Second
	}
	if len(be.Percents) == 0 {
		be.Percents = []int{50, 75, 90, 95, 99}
	}

	if be.Limit > 0 {
		limiter := rate.NewLimiter(rate.Every(1*time.Second), be.Limit)
		be.limitFn = func() {
			limiter.Wait(context.Background())
		}
	}

	be.pbuffers = make([][]byte, 1024)
	be.wbuffers = make([][]byte, 1024)
	for i := 0; i < len(be.pbuffers); i++ {
		buffer := make([]byte, be.Payload)
		rand.Read(buffer)
		be.pbuffers[i] = buffer
		be.wbuffers[i] = protocol.EncodeClientMessage(websocket.BinaryMessage, buffer)
	}

	be.chConns = make(chan *websocket.Conn, len(be.ConnsMap))
	for c := range be.ConnsMap {
		be.chConns <- c
	}

	psCounter, err := perf.NewPSCounter(be.ServerPid)
	if err != nil {
		logging.Printf("perf.NewPSCounter failed: %v", err)
	} else {
		be.PsCounter = psCounter
	}

	be.Calculator = perf.NewCalculator(fmt.Sprintf("%v-TPS", be.Framework))
}

func (be *BenchEcho) clean() {
	be.chConns = nil
	be.wbuffers = nil
	be.bufferIdx = 0
	be.limitFn = func() {}
}

func (be *BenchEcho) getBuffers() ([]byte, []byte) {
	idx := uint32(mrand.Intn(len(be.wbuffers))) % uint32(len(be.wbuffers))
	return be.pbuffers[idx], be.wbuffers[idx]
}

func (be *BenchEcho) doOnce() error {
	conn := <-be.chConns
	defer func() {
		be.chConns <- conn
	}()

	be.limitFn()

	pbuffer, wbuffer := be.getBuffers()
	_, err := conn.UnderlyingConn().Write(wbuffer)
	if err != nil {
		return err
	}

	nread := 0
	rbuffer := be.rbufferPool.Get().(*[]byte)
	defer be.rbufferPool.Put(rbuffer)
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

	if be.checkValid {
		if mt != websocket.BinaryMessage {
			return errors.New("invalid message type")
		}
		if !bytes.Equal(pbuffer, readBuffer[:nread]) {
			return errors.New("respons data is not equal to origin")
		}
	}

	return nil
}
