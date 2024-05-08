package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
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
	_ = flag.Bool("nodelay", true, `tcp nodelay`)

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
	payload    = flag.Int("b", 1024, `benchmark: payload size of benchecho and benchrate`)
	checkValid = flag.Bool("check", false, `benchmark: whether to check the validity of the response data`)
	psInterval = flag.Int("pi", 1000, `benchmark: ps interval of benchecho and benchrate, 1000 ms by default`)

	// BenchEcho
	echoConcurrency   = flag.Int("ec", 10000, "benchecho: concurrency: how many goroutines used to do the echo test")
	echoTimes         = flag.Int("en", 2000000, `benchecho: benchmark times`)
	echoTPSLimit      = flag.Int("el", 0, `benchecho: TPS limitation per second`)
	echoPprof         = flag.Bool("ep", true, `benchecho: generate pprof report`)
	echoPprofDuration = flag.Int("epd", 5, `benchecho: pprof duration`)

	// BenchRate
	rateEnabled       = flag.Bool("rate", false, `benchrate: whether run benchrate`)
	rateConcurrency   = flag.Int("rc", 10000, "benchrate: concurrency: how many goroutines used to do the echo test")
	rateDuration      = flag.Int("rd", 10, `benchrate: how long to spend to do the test`)
	rateSendRate      = flag.Int("rr", 200, "benchrate: how many request message can be sent to 1 conn every second")
	rateBatchSize     = flag.Int("rbs", 1024*16, "benchrate: how many bytes can be written to 1 conn every time")
	rateSendLimit     = flag.Int("rl", 0, `benchrate: message sending limitation per second`)
	ratePprof         = flag.Bool("rp", false, `benchrate: generate pprof report`)
	ratePprofDuration = flag.Int("rpd", 5, `benchrate: pprof duration`)

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
	csReport := cs.Report()
	report.ToFile(csReport, *preffix, *suffix)
	logging.Print(logging.ShortLine)
	logging.Print(csReport.String())
	logging.Print("\n")
	logging.Print(logging.ShortLine)

	cpuProfileUrlEcho := ""
	cpuProfileUrlRate := ""
	memProfileUrl := ""
	serverPid, pprofAddr, err := config.InitAndGetFrameworkPid(*framework, *ip, &config.InitArgs{
		PsInterval: time.Millisecond * time.Duration(*psInterval),
	})
	if err != nil {
		logging.Printf("InitAndGetFrameworkPid(%v) failed: %v", *framework, err)
	} else {
		cpuProfileUrl := pprofAddr + "/debug/pprof/profile"
		cpuProfileUrlEcho = cpuProfileUrl + fmt.Sprintf("?seconds=%v", *echoPprofDuration)
		cpuProfileUrlRate = cpuProfileUrl + fmt.Sprintf("?seconds=%v", *ratePprofDuration)
		fmt.Printf("pprof cpu :\n  curl --output ./cpu_profile %v\n", cpuProfileUrl)
		fmt.Printf("  go tool pprof -http=:6060 ./cpu_profile\n")
		memProfileUrl = pprofAddr + "/debug/pprof/heap"
		fmt.Printf("pprof heap:\n  curl --output ./mem_profile %v\n", memProfileUrl)
		fmt.Printf("  go tool pprof -http=:6061 ./mem_profile\n")
		logging.Print(logging.ShortLine)
	}
	be := benchecho.New(*framework, serverPid, *echoTimes, *ip, cs.Conns(), *checkValid)
	be.Concurrency = *echoConcurrency
	be.Payload = *payload
	be.Total = *echoTimes
	be.Limit = *echoTPSLimit
	if *echoPprof {
		be.OnWarmup(func() {
			time.AfterFunc(time.Second*2, func() {
				cpu, err := httpGet(cpuProfileUrlEcho)
				if err != nil {
					fmt.Printf("BenchEcho: [pprof cpu] httpGet failed: %v\n", err)
					return
				}

				mem, err := httpGet(memProfileUrl)
				if err != nil {
					fmt.Printf("BenchEcho: [pprof mem] httpGet failed: %v\n", err)
					return
				}
				be.SetPprofData(cpu, mem)
			})
		})
	}
	be.Run()
	defer be.Stop()
	beReport := be.Report()
	report.ToFile(beReport, *preffix, *suffix)
	logging.Print(logging.ShortLine)
	logging.Print(beReport.String())
	logging.Print("\n")
	logging.Print(logging.ShortLine)

	if *rateEnabled {
		br := benchrate.New(*framework, serverPid, *ip, cs.Options, cs.NBConns(), *checkValid)
		br.Concurrency = *rateConcurrency
		br.Duration = time.Second * time.Duration(*rateDuration)
		br.SendRate = *rateSendRate
		br.BatchSize = *rateBatchSize
		br.Payload = *payload
		br.SendLimit = *rateSendLimit
		if *ratePprof {
			br.OnBenchmark(func() {
				time.AfterFunc(time.Second*2, func() {
					cpu, err := httpGet(cpuProfileUrlRate)
					if err != nil {
						fmt.Printf("BenchRate: [pprof cpu] httpGet failed: %v\n", err)
						return
					}

					mem, err := httpGet(memProfileUrl)
					if err != nil {
						fmt.Printf("BenchRate: [pprof mem] httpGet failed: %v\n", err)
						return
					}
					br.SetPprofData(cpu, mem)
				})
			})
		}
		br.Run()
		defer br.Stop()
		brReport := br.Report()
		report.ToFile(brReport, *preffix, *suffix)
		logging.Print(logging.ShortLine)
		logging.Print(brReport.String())
		logging.Print("\n")
		logging.Print(logging.ShortLine)
	}
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
	logging.Printf("[%vBenchRate%v] Report\n", *preffix, *suffix)
	logging.Print(data)
	logging.Print(logging.LongLine)
}

func httpGet(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if res != nil && res.Body != nil {
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, err
		}
		return body, nil
	}
	return nil, err
}
