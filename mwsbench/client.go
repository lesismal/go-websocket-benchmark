package main

import (
	"flag"
	"runtime/debug"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/mwsbench/benchecho"
	"go-websocket-benchmark/mwsbench/benchrate"
	"go-websocket-benchmark/mwsbench/connections"
	"go-websocket-benchmark/mwsbench/report"
)

var (
	// Client Proc
	memLimit = flag.Int64("m", 1024*1024*1024*4, `memory limit`)

	// Server Side
	framework = flag.String("f", config.NbioStd, `framework, e.g. "gorilla"`)
	ip        = flag.String("ip", "127.0.0.1", `ip, e.g. "127.0.0.1"`)

	// Connection
	numConnections    = flag.Int("c", 10000, "client: num of connections")
	dialConcurrency   = flag.Int("dc", 2000, "client: dial concurrency: how many goroutines used to do dialing")
	dialTimeout       = flag.Duration("dt", 5*time.Second, "client: dial timeout")
	dialRetries       = flag.Int("dr", 5, "client: dial retry times")
	dialRetryInterval = flag.Duration("dri", 100*time.Millisecond, "client; dial retry interval")

	// BenchEcho && BenchRate
	payload = flag.Int("b", 1024, `benchmark: payload size of benchecho and benchrate`)

	// BenchEcho
	echoConcurrency = flag.Int("ec", 50000, "benchecho: concurrency: how many goroutines used to do the echo test")
	echoTimes       = flag.Int("en", 1000, `benchecho: benchmark times`)
	echoTPSLimit    = flag.Int("el", 0, `benchecho: TPS limitation per second`)

	// BenchRate
	rateConnConcurrency = flag.Int("rc", 10, "benchrate: how many request message can be sent to 1 conn before response")
	rateDuration        = flag.Duration("rd", time.Second*10, `benchrate: how long to spend to do the test`)
	rateSendLimit       = flag.Int("rl", 0, `benchrate: message sending limitation per second`)

	// for report generation
	genReport = flag.Bool("r", false, `make report`)
	preffix   = flag.String("preffix", "", `report file preffix, e.g. "1m_connections_"`)
	suffix    = flag.String("suffix", "", `report file suffix, e.g. "_20060102150405"`)
)

func main() {
	flag.Parse()

	if *genReport {
		generateReports()
		return
	}

	debug.SetMemoryLimit(*memLimit)

	logging.Print(logging.LongLine)
	defer logging.Print(logging.LongLine)

	logging.Printf("Benchmark [%v]: %v connections, %v payload, %v times", *framework, *numConnections, *payload, *echoTimes)
	logging.Print(logging.ShortLine)

	cs := connections.New(*framework, *ip, *numConnections)
	cs.Concurrency = *dialConcurrency
	cs.DialTimeout = *dialTimeout
	cs.RetryTimes = *dialRetries
	cs.RetryInterval = *dialRetryInterval
	cs.Run()
	defer cs.Stop()

	bm := benchecho.New(*framework, *echoTimes, *ip, cs.Conns())
	bm.Concurrency = *echoConcurrency
	bm.Payload = *payload
	bm.Total = *echoTimes
	bm.Limit = *echoTPSLimit
	bm.Run()
	defer bm.Stop()

	br := benchrate.New(*framework, *ip, cs.NBConns())
	br.ConnConcurrency = *rateConnConcurrency
	br.Payload = *payload
	br.Duration = *rateDuration
	br.SendLimit = *rateSendLimit
	br.Run()
	defer br.Stop()

	csReport := cs.Report()
	report.ToFile(csReport, *preffix, *suffix)

	bmReport := bm.Report()
	report.ToFile(bmReport, *preffix, *suffix)

	brReport := br.Report()
	report.ToFile(brReport, *preffix, *suffix)

	logging.Print(logging.ShortLine)
	logging.Print(csReport.String())
	logging.Print("\n")
	logging.Print(logging.ShortLine)
	logging.Print(bmReport.String())
	logging.Print("\n")
	logging.Print(logging.ShortLine)
	logging.Print(brReport.String())
	logging.Print("\n")
}

func generateReports() {
	data := report.GenerateConnectionsReports(*preffix, *suffix, nil)
	filename := report.Filename("Connections", *preffix, *suffix+".md")
	report.WriteFile(filename, data)
	logging.Print(logging.LongLine)
	logging.Printf("[%vConnections%v] Report\n", *preffix, *suffix)
	logging.Print(data)

	data = report.GenerateBenchEchoReports(*preffix, *suffix, nil)
	filename = report.Filename("BenchEcho", *preffix, *suffix+".md")
	report.WriteFile(filename, data)
	logging.Print(logging.LongLine)
	logging.Printf("[%vBenchEcho%v] Report\n", *preffix, *suffix)
	logging.Print(data)
	logging.Print(logging.LongLine)

	data = report.GenerateBenchRateReports(*preffix, *suffix, nil)
	filename = report.Filename("BenchRate", *preffix, *suffix+".md")
	report.WriteFile(filename, data)
	logging.Print(logging.LongLine)
	logging.Printf("[%BenchRate%v] Report\n", *preffix, *suffix)
	logging.Print(data)
	logging.Print(logging.LongLine)
}
