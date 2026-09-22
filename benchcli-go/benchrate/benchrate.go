package benchrate

import (
	"bytes"
	"context"
	"crypto/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"go-websocket-benchmark/benchcli-go/protocol"
	"go-websocket-benchmark/benchcli-go/report"
	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"

	"github.com/lesismal/nbio/nbhttp/websocket"
	"github.com/lesismal/perf"
	"golang.org/x/time/rate"
)

type BenchRate struct {
	Framework   string
	Ip          string
	Duration    time.Duration
	Concurrency int
	SendRate    int
	BatchSize   int
	Payload     int
	SendLimit   int
	PsInterval  time.Duration

	ServerPid int
	PsCounter *perf.PSCounter
	// Where the CPU and MEM columns come from; see config.SetupPS.
	PsSource config.PSSource

	wsOptions *websocket.Options
	ConnsMap  map[*websocket.Conn]struct{}

	wbuffer []byte

	chConns chan *websocket.Conn

	limitFn    func()
	checkValid bool

	batch       int
	batchBuffer []byte
	tickRate    int

	sendTimes int64
	sendBytes int64
	recvTimes int64
	recvBytes int64

	onBenchmark  func()
	pprofDataCPU []byte `json:"-" md:"-"`
	pprofDataMEM []byte `json:"-" md:"-"`
}

type Conn struct {
	net.Conn
	sendCnt int64
	recvCnt int64
}

func New(framework string, serverPid int, ip string, options *websocket.Options, connsMap map[*websocket.Conn]struct{}, checkValid bool) *BenchRate {
	bm := &BenchRate{
		Framework:  framework,
		Ip:         ip,
		ConnsMap:   connsMap,
		limitFn:    func() {},
		checkValid: checkValid,
		wsOptions:  options,
		ServerPid:  serverPid,
	}
	return bm
}

func (br *BenchRate) Run() {
	br.init()
	defer br.clean()

	// chCounterStart := make(chan struct{})
	// go func() {
	// 	if br.PsCounter != nil {
	// 		br.PsCounter.Start(perf.PSCountOptions{
	// 			CountCPU: true,
	// 			CountMEM: true,
	// 			CountIO:  true,
	// 			CountNET: true,
	// 			Interval: br.PsInterval,
	// 		})
	// 		time.Sleep(br.PsInterval)
	// 		close(chCounterStart)
	// 	}
	// }()

	done := make(chan struct{})
	time.AfterFunc(br.Duration, func() {
		close(done)
	})

	logging.Printf("BenchRate for %.2f seconds ...", br.Duration.Seconds())

	wg := sync.WaitGroup{}

	connTeams := make([][]*Conn, br.Concurrency)
	cnt := 0
	for wsc := range br.ConnsMap {
		cnt++
		idx := cnt % len(connTeams)
		conn := &Conn{
			Conn: wsc.Conn,
		}
		connTeams[idx] = append(connTeams[idx], conn)
		wsc.SetSession(conn)
	}

	if br.onBenchmark != nil {
		br.onBenchmark()
	}

	for i := 0; i < br.Concurrency; i++ {
		wg.Add(1)
		conns := connTeams[i]
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(time.Second / time.Duration(br.tickRate))
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					br.doOnce(conns)
				}
			}
		}()
	}
	wg.Wait()

	logging.Printf("BenchRate for %.2f seconds done", br.Duration.Seconds())

	// if br.PsCounter != nil {
	// 	<-chCounterStart
	// 	br.PsCounter.Stop()
	// }
}

func (br *BenchRate) Stop() {

}

func (br *BenchRate) OnBenchmark(f func()) {
	br.onBenchmark = f
}

func (br *BenchRate) SetPprofData(cpu, mem []byte) {
	br.pprofDataCPU = cpu
	br.pprofDataMEM = mem
}

func (br *BenchRate) Report() *report.BenchRateReport {
	r := &report.BenchRateReport{
		BenchClient: "benchcli-go",
		Framework:   br.Framework,
		Duration:    br.Duration.Nanoseconds(),
		Connections: len(br.ConnsMap),
		SendRate:    br.SendRate,
		Payload:     br.Payload,
		SendTimes:   br.sendTimes,
		SendBytes:   br.sendBytes,
		RecvTimes:   br.recvTimes,
		RecvBytes:   br.recvBytes,
	}
	r.SetPprofData(br.pprofDataCPU, br.pprofDataMEM)
	r.TaskPool = config.GetFrameworkTaskPool(br.Framework, br.Ip)
	var psErr error
	br.PsCounter, psErr = br.psInfo()
	if psErr != nil {
		logging.Printf("BenchRate: resource statistics for %v incomplete, EchoEER will read 0: %v",
			br.Framework, psErr)
	}
	if br.PsCounter != nil {
		// r.GoMin = br.PsCounter.NumGoroutineMin()
		// r.GoAvg = br.PsCounter.NumGoroutineAvg()
		// r.GoMax = br.PsCounter.NumGoroutineMax()
		r.CPUMin = br.PsCounter.CPUMin()
		r.CPUAvg = br.PsCounter.CPUAvg()
		r.CPUMax = br.PsCounter.CPUMax()
		r.MEMRSSMin = br.PsCounter.MEMRSSMin()
		r.MEMRSSAvg = br.PsCounter.MEMRSSAvg()
		r.MEMRSSMax = br.PsCounter.MEMRSSMax()
		// In floating point: r.Duration is nanoseconds, and an integer
		// division by the second first would make every sub-second run a
		// division by zero.
		r.EchoEER = report.EER(float64(r.RecvTimes)/(float64(r.Duration)/float64(time.Second)), r.CPUAvg)
	}
	return r
}

// psInfo reads the server's resource samples from wherever this run takes
// them; see BenchEcho.psInfo.
func (br *BenchRate) psInfo() (*perf.PSCounter, error) {
	if br.PsSource != nil {
		return br.PsSource.PsInfo()
	}
	return config.GetFrameworkPsInfo(br.Framework, br.Ip)
}

func (br *BenchRate) init() {
	if br.Duration <= 0 {
		br.Duration = time.Second * 10
	}
	if br.Concurrency <= 0 {
		br.Concurrency = 50000
	}
	if br.Concurrency > len(br.ConnsMap) {
		br.Concurrency = len(br.ConnsMap)
	}
	if br.SendRate <= 0 {
		br.SendRate = 1
	}
	if br.Payload <= 0 {
		br.Payload = 1024
	}

	br.wbuffer = make([]byte, br.Payload)
	rand.Read(br.wbuffer)
	message := protocol.EncodeClientMessage(websocket.BinaryMessage, br.wbuffer)
	br.batchBuffer, br.batch, br.tickRate = protocol.BatchBuffers(message, br.SendRate, br.BatchSize)
	// br.batchBuffer, br.batch, br.tickRate = message, 1, br.SendRate
	if br.tickRate <= 0 || len(br.batchBuffer) == 0 {
		logging.Fatalf("BenchRate get wrong tickRate: %v, or batchBuffer: %v", br.tickRate, len(br.batchBuffer))
	}

	if br.PsInterval <= 0 {
		br.PsInterval = time.Second
	}

	if br.SendLimit > 0 {
		limiter := rate.NewLimiter(rate.Every(1*time.Second), br.SendLimit)
		br.limitFn = func() {
			limiter.WaitN(context.Background(), len(br.batchBuffer)/br.Payload)
		}
	}

	br.wsOptions.OnMessage(br.onMessage)
	br.chConns = make(chan *websocket.Conn, len(br.ConnsMap)*br.SendRate)
	for i := 0; i < br.SendRate; i++ {
		for c := range br.ConnsMap {
			br.chConns <- c
		}
	}

	// psCounter, err := perf.NewPSCounter(br.ServerPid)
	// if err != nil {
	// 	logging.Printf("perf.NewPSCounter failed: %v", err)
	// } else {
	// 	br.PsCounter = psCounter
	// }
}

func (br *BenchRate) clean() {
	br.chConns = nil
	br.limitFn = func() {}
}

func (br *BenchRate) getWriteBuffer() []byte {
	return br.wbuffer
}

func (br *BenchRate) doOnce(conns []*Conn) {
	for _, conn := range conns {
		if atomic.LoadInt64(&conn.sendCnt)-atomic.LoadInt64(&conn.recvCnt)+int64(br.batch) < int64(br.batch*5) {
			br.limitFn()
			_, err := conn.Write(br.batchBuffer)
			if err == nil {
				atomic.AddInt64(&br.sendTimes, int64(br.batch))
				atomic.AddInt64(&br.sendBytes, int64(br.batch*br.Payload))
				atomic.AddInt64(&conn.sendCnt, int64(br.batch))
			}
		}
	}
}

func (br *BenchRate) onMessage(c *websocket.Conn, mt websocket.MessageType, b []byte) {
	if !br.checkValid {
		conn := c.Session().(*Conn)
		atomic.AddInt64(&conn.recvCnt, 1)
		atomic.AddInt64(&br.recvTimes, 1)
		atomic.AddInt64(&br.recvBytes, int64(len(b)))
		return
	}

	if mt == websocket.BinaryMessage && bytes.Equal(b, br.getWriteBuffer()) {
		conn := c.Session().(*Conn)
		atomic.AddInt64(&conn.recvCnt, 1)
		atomic.AddInt64(&br.recvTimes, 1)
		atomic.AddInt64(&br.recvBytes, int64(len(b)))
	}
}
