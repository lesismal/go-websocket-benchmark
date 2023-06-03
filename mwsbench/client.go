package main

import (
	"flag"
	"runtime/debug"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/mwsbench/benchecho"
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
	numConnections    = flag.Int("c", 5000, "client num")
	dialConcurrency   = flag.Int("dc", 2000, "goroutine num")
	dialTimeout       = flag.Duration("dt", 5*time.Second, "client dial timeout")
	dialRetries       = flag.Int("dr", 5, "client dial retry count")
	dialRetryInterval = flag.Duration("dri", 100*time.Millisecond, "client dial retry interval")

	// BenchEcho
	echoConcurrency = flag.Int("bc", 5000, "goroutine num")
	payload         = flag.Int("b", 1024, `payload size`)
	echoTimes       = flag.Int("n", 1000000, `benchmark times`)
	tpsLimit        = flag.Int("l", 0, `max benchmark tps`)

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

	bm := benchecho.New(*framework, *echoTimes, *ip, cs.ConnsMap)
	bm.Concurrency = *echoConcurrency
	bm.Payload = *payload
	bm.Total = *echoTimes
	bm.Limit = *tpsLimit
	bm.Run()
	defer bm.Stop()

	csReport := cs.Report()
	report.ToFile(csReport, *preffix, *suffix)

	bmReport := bm.Report()
	report.ToFile(bmReport, *preffix, *suffix)

	logging.Print(logging.ShortLine)
	logging.Print(csReport.String())
	logging.Print(logging.ShortLine)
	logging.Print(bmReport.String())
}

func generateReports() {
	data := report.GenerateConnectionsReports(*preffix, *suffix)
	filename := report.Filename("Connections", *preffix, *suffix+".md")
	report.WriteFile(filename, data)
	logging.Print(logging.LongLine)
	logging.Print("\n")
	logging.Print(data)
	logging.Print("\n")

	data = report.GenerateBenchEchoReports(*preffix, *suffix)
	filename = report.Filename("BenchEcho", *preffix, *suffix+".md")
	report.WriteFile(filename, data)
	logging.Print(logging.LongLine)
	logging.Print("\n")
	logging.Print(data)
	logging.Print("\n")
	logging.Print(logging.LongLine)
}
