package benchecho

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync/atomic"
	"time"

	"go-websocket-benchmark/config"

	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/nbhttp/websocket"
	"github.com/lesismal/perf"
	"golang.org/x/time/rate"
)

type BenchEcho struct {
	Framework   string
	Ip          string
	WarmupTimes int
	Times       int
	Concurrency int
	Payload     int
	Limit       int
	Percents    []int
	PsInterval  time.Duration

	OutPreffix string
	OutSuffix  string

	Calculator *perf.Calculator
	PsCounter  *perf.PSCounter

	buffers   [][]byte
	bufferIdx uint32

	chConns chan *websocket.Conn
	conns   map[*websocket.Conn]struct{}

	limitFn func()
}

func New(framework, ip string, concurrency, nWarmup, nBenchmark int, conns map[*websocket.Conn]struct{}) *BenchEcho {
	bm := &BenchEcho{
		Framework:   framework,
		Ip:          ip,
		Concurrency: concurrency,
		WarmupTimes: nWarmup,
		Times:       nBenchmark,

		conns:   conns,
		limitFn: func() {},
	}
	return bm
}

func (bm *BenchEcho) Run() {
	bm.init()
	defer bm.clean()

	fmt.Printf("warmup for %d times ...\n", bm.WarmupTimes)
	bm.Calculator.Warmup(bm.Concurrency, bm.WarmupTimes, bm.doOnce)
	fmt.Printf("warmup for %d times done\n", bm.WarmupTimes)

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

	log.Printf("benchmark start ...")
	bm.Calculator.Benchmark(bm.Concurrency, bm.Times, bm.doOnce, bm.Percents)
	log.Printf("benchmark done")

	<-chCounterStart
	bm.PsCounter.Stop()

	fmt.Println("-------------------------")
	fmt.Printf("Benchmark: %s\n", bm.Framework)
	fmt.Printf("Conns    : %d\n", len(bm.conns))
	fmt.Printf("Payload  : %d\n", bm.Payload)
	fmt.Println(bm.Calculator.String())
	fmt.Printf(`CPU MIN  : %.2f%%
CPU AVG  : %.2f%%
CPU MAX  : %.2f%%
MEM MIN  : %v
MEM AVG  : %v
MEM MAX  : %v
`,
		bm.PsCounter.CPUMin(),
		bm.PsCounter.CPUAvg(),
		bm.PsCounter.CPUMax(),
		perf.I2MemString(bm.PsCounter.MEMRSSMin()),
		perf.I2MemString(bm.PsCounter.MEMRSSAvg()),
		perf.I2MemString(bm.PsCounter.MEMRSSMax()))

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
	// 	log.Fatalf("Marshal Report failed: %v", err)
	// }
	// err = os.WriteFile("./output/report/"+*preffix+*framework+*suffix+".json", b, 0666)
	// if err != nil {
	// 	log.Fatalf("Write Report failed: %v", err)
	// }
	// fmt.Println("-------------------------")

}

func (bm *BenchEcho) Stop() {

}

func (bm *BenchEcho) init() {
	if bm.WarmupTimes <= 0 {
		bm.WarmupTimes = len(bm.conns) * 2
	}
	if bm.Concurrency <= 0 {
		bm.Concurrency = runtime.NumCPU() * 1024
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

	bm.chConns = make(chan *websocket.Conn, len(bm.conns))
	for c := range bm.conns {
		c.SetSession(make(chan config.EchoSession, 1))
		c.OnMessage(bm.onMessage)
		bm.chConns <- c
	}

	serverPid, err := config.GetFrameworkPid(bm.Framework, bm.Ip)
	if err != nil {
		log.Fatalf("BenchEcho GetFrameworkPid failed: %v", err)
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

// func makeReport(typ string) {
// 	fmt.Println("simple report:")
// 	fmt.Println("")
// 	makeReportMarkdown(true)
// 	fmt.Println("")
// 	fmt.Println("full report:")
// 	fmt.Println("")
// 	makeReportMarkdown(false)
// }

// func makeReportMarkdown(simple bool) {
// 	reports := make([]Report, len(config.FrameworkList))[:0]
// 	for _, v := range config.FrameworkList {
// 		b, err := os.ReadFile("./output/report/" + *preffix + v + *suffix + ".json")
// 		if err != nil {
// 			continue
// 			// log.Fatalf("Read Report %v failed: %v", v, err)
// 		}

// 		report := &FullReport{}
// 		err = json.Unmarshal(b, report)
// 		if err != nil {
// 			continue
// 			// log.Fatalf("Unmarshal Report %v failed: %v", v, err)
// 		}
// 		if simple {
// 			reports = append(reports, report.ToSimple())
// 		} else {
// 			reports = append(reports, report)
// 		}
// 	}

// 	table := perf.NewTable()
// 	if simple {
// 		table.SetTitle((&SimpleReport{}).Headers())
// 	} else {
// 		table.SetTitle((&FullReport{}).Headers())
// 	}

// 	for _, v := range reports {
// 		table.AddRow(v.Strings())
// 	}

// 	text := table.Markdown()
// 	fmt.Println(text)
// 	if simple {
// 		err := os.WriteFile("./output/report/"+*preffix+"report_simple"+*suffix+".md", []byte(text), 0666)
// 		if err != nil {
// 			log.Fatalf("Write Report failed: %v", err)
// 		}
// 	} else {
// 		err := os.WriteFile("./output/report/"+*preffix+"report_full"+*suffix+".md", []byte(text), 0666)
// 		if err != nil {
// 			log.Fatalf("Write Report failed: %v", err)
// 		}
// 	}
// }
