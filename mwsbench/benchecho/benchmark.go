package benchecho

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/mwsbench/report"

	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/nbhttp/websocket"
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

	OutPreffix string
	OutSuffix  string

	Calculator *perf.Calculator
	PsCounter  *perf.PSCounter

	ConnsMap map[*websocket.Conn]struct{}

	buffers   [][]byte
	bufferIdx uint32

	chConns chan *websocket.Conn

	limitFn func()
}

func New(framework string, benchmarkTimes int, ip string, connsMap map[*websocket.Conn]struct{}) *BenchEcho {
	bm := &BenchEcho{
		Framework: framework,
		Ip:        ip,
		Total:     benchmarkTimes,
		ConnsMap:  connsMap,
		limitFn:   func() {},
	}
	return bm
}

func (bm *BenchEcho) Run() {
	bm.init()
	defer bm.clean()

	logging.Printf("warmup for %d times ...\n", bm.WarmupTimes)
	bm.Calculator.Warmup(bm.Concurrency, bm.WarmupTimes, bm.doOnce)
	logging.Printf("warmup for %d times done\n", bm.WarmupTimes)

	// delay 1 second
	chCounterStart := make(chan struct{})
	time.AfterFunc(time.Second, func() {
		bm.PsCounter.Start(perf.PSCountOptions{
			CountCPU: true,
			CountMEM: true,
			CountIO:  true,
			CountNET: true,
			Interval: bm.PsInterval,
		})
		time.Sleep(bm.PsInterval)
		close(chCounterStart)
	})

	logging.Printf("benchmark for %d times ...\n", bm.Total)
	bm.Calculator.Benchmark(bm.Concurrency, bm.Total, bm.doOnce, bm.Percents)
	logging.Printf("benchmark for %d times done\n", bm.Total)

	<-chCounterStart
	bm.PsCounter.Stop()

	// 	logging.Printf("Benchmark: %s\n", bm.Framework)
	// 	logging.Printf("Conns    : %d\n", len(bm.ConnsMap))
	// 	logging.Printf("Payload  : %d\n", bm.Payload)
	// 	logging.Println(bm.Calculator.String())
	// 	logging.Printf(`CPU MIN  : %.2f%%
	// CPU AVG  : %.2f%%
	// CPU MAX  : %.2f%%
	// MEM MIN  : %v
	// MEM AVG  : %v
	// MEM MAX  : %v
	// `,
	// 		bm.PsCounter.CPUMin(),
	// 		bm.PsCounter.CPUAvg(),
	// 		bm.PsCounter.CPUMax(),
	// 		perf.I2MemString(bm.PsCounter.MEMRSSMin()),
	// 		perf.I2MemString(bm.PsCounter.MEMRSSAvg()),
	// 		perf.I2MemString(bm.PsCounter.MEMRSSMax()))

	// report := &FullReport{
	// 	Framework:   *framework,
	// 	Connections: *numClient,
	// 	Payload:     *payloadSize,
	// 	Total:       int64(bm.Calculator.Total),
	// 	Success:     bm.Calculator.Success,
	// 	Failed:      bm.Calculator.Failed,
	// 	TimeUsed:    bm.Calculator.Used,
	// 	Min:         bm.Calculator.Min,
	// 	Avg:         bm.Calculator.Avg,
	// 	Max:         bm.Calculator.Max,
	// 	TPS:         bm.Calculator.TPS(),
	// 	TP50:        bm.Calculator.TPN(50),
	// 	TP75:        bm.Calculator.TPN(75),
	// 	TP90:        bm.Calculator.TPN(90),
	// 	TP95:        bm.Calculator.TPN(95),
	// 	TP99:        bm.Calculator.TPN(99),
	// 	CPUMin:      psCounter.CPUMin(),
	// 	CPUAvg:      psCounter.CPUAvg(),
	// 	CPUMax:      psCounter.CPUMax(),
	// 	MEMRSSMin:   psCounter.MEMRSSMin(),
	// 	MEMRSSAvg:   psCounter.MEMRSSAvg(),
	// 	MEMRSSMax:   psCounter.MEMRSSMax(),
	// }
	// b, err := json.Marshal(report)
	// if err != nil {
	// 	logging.Fatalf("Marshal Report failed: %v", err)
	// }
	// err = os.WriteFile("./output/report/"+*preffix+*framework+*suffix+".json", b, 0666)
	// if err != nil {
	// 	logging.Fatalf("Write Report failed: %v", err)
	// }
	// fmt.Println("-------------------------")

}

func (bm *BenchEcho) Stop() {

}

func (bm *BenchEcho) Report() report.Report {
	return &report.BenchEchoReport{
		Framework:   bm.Framework,
		Connections: len(bm.ConnsMap),
		Concurrency: bm.Concurrency,
		Payload:     bm.Payload,
		Total:       bm.Total,
		Success:     bm.Calculator.Success,
		Failed:      bm.Calculator.Failed,
		Used:        int64(bm.Calculator.Used),
		CPUMin:      bm.PsCounter.CPUMin(),
		CPUAvg:      bm.PsCounter.CPUAvg(),
		CPUMax:      bm.PsCounter.CPUMax(),
		MEMRSSMin:   bm.PsCounter.MEMRSSMin(),
		MEMRSSAvg:   bm.PsCounter.MEMRSSAvg(),
		MEMRSSMax:   bm.PsCounter.MEMRSSMax(),
		TPS:         bm.Calculator.TPS(),
		Min:         bm.Calculator.Min,
		Avg:         bm.Calculator.Avg,
		Max:         bm.Calculator.Max,
		TP50:        bm.Calculator.TPN(50),
		TP75:        bm.Calculator.TPN(75),
		TP90:        bm.Calculator.TPN(90),
		TP95:        bm.Calculator.TPN(95),
		TP99:        bm.Calculator.TPN(99),
	}
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
	if bm.Payload <= 0 {
		bm.Payload = 1024
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

	bm.buffers = make([][]byte, 1024)
	for i := 0; i < len(bm.buffers); i++ {
		buffer := make([]byte, bm.Payload)
		rand.Read(buffer)
		bm.buffers[i] = buffer
	}

	bm.chConns = make(chan *websocket.Conn, len(bm.ConnsMap))
	for c := range bm.ConnsMap {
		c.SetSession(make(chan config.EchoSession, 1))
		c.OnMessage(bm.onMessage)
		bm.chConns <- c
	}

	serverPid, err := config.GetFrameworkPid(bm.Framework, bm.Ip)
	if err != nil {
		logging.Fatalf("BenchEcho GetFrameworkPid(%v) failed: %v", bm.Framework, err)
	}
	psCounter, err := perf.NewPSCounter(serverPid)
	if err != nil {
		panic(err)
	}
	bm.PsCounter = psCounter

	bm.Calculator = perf.NewCalculator(fmt.Sprintf("%v-TPS", bm.Framework))
}

func (bm *BenchEcho) clean() {
	bm.chConns = nil
	bm.buffers = nil
	bm.bufferIdx = 0
	bm.limitFn = func() {}
}

func (bm *BenchEcho) onMessage(c *websocket.Conn, mt websocket.MessageType, b []byte) {
	ch, _ := c.Session().(chan config.EchoSession)
	ch <- config.EchoSession{
		MT:    mt,
		Bytes: b,
	}
}

func (bm *BenchEcho) getBuffer() []byte {
	return bm.buffers[atomic.AddUint32(&bm.bufferIdx, 1)%uint32(len(bm.buffers))]
}

func (bm *BenchEcho) doOnce() error {
	conn := <-bm.chConns
	defer func() {
		bm.chConns <- conn
	}()

	bm.limitFn()

	buffer := bm.getBuffer()
	err := conn.WriteMessage(websocket.BinaryMessage, buffer)
	if err != nil {
		return err
	}
	chResponse := conn.Session().(chan config.EchoSession)
	echo := <-chResponse
	defer mempool.Free(echo.Bytes)
	if echo.MT != websocket.BinaryMessage {
		return errors.New("invalid message type")
	}
	if !bytes.Equal(buffer, echo.Bytes) {
		return errors.New("respons data is not equal to origin")
	}

	return nil
}
