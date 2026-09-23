package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"go-websocket-benchmark/benchcli-go/benchecho"
	"go-websocket-benchmark/benchcli-go/benchrate"
	"go-websocket-benchmark/benchcli-go/connections"
	"go-websocket-benchmark/benchcli-go/report"
	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"
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
	psMode     = flag.String("ps", config.PSModeAuto, `benchmark: where the server's CPU and MEM samples come from: `+
		`"auto" samples the server here when it runs on this machine and asks it over HTTP when it does not, `+
		`"local" always samples here, "remote" always asks`)
	enableTPN = flag.Bool("tpn", true, `benchmark: whether enable TPN caculation`)

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
	genReport  = flag.Bool("r", false, `make report`)
	preffix    = flag.String("preffix", "", `report file preffix, e.g. "1m_connections_"`)
	suffix     = flag.String("suffix", "", `report file suffix, e.g. "_20060102150405"`)
	reportSort = flag.String("sort", report.DefaultSort, `report row order: "result" ranks the best result first, "framework" keeps the framework order`)
)

func main() {
	wd, _ := os.Getwd()
	logging.Println("pwd:", wd)

	flag.Parse()

	report.Init(*enableTPN)

	// Checked even when no report is being generated, so that a run started
	// with a misspelled -sort fails before it spends the benchmark rather
	// than after.
	if err := report.ValidateSort(*reportSort); err != nil {
		logging.Fatalf("%v", err)
	}
	if err := config.ValidatePSMode(*psMode); err != nil {
		logging.Fatalf("%v", err)
	}

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
	cs.EnalbeTPN = *enableTPN
	cs.Run()
	defer cs.Stop()
	csReport := cs.Report()
	saveReport(csReport)
	logging.Print(logging.ShortLine)
	logging.Print(csReport.String(*enableTPN))
	logging.Print("\n")
	logging.Print(logging.ShortLine)

	cpuProfileUrlEcho := ""
	cpuProfileUrlRate := ""
	memProfileUrl := ""
	// How the server's CPU and MEM - and so EER - are sampled. On a run whose
	// server is on this machine the client samples the process itself and the
	// server is never asked, which is one less request to fail at the far end
	// of a benchmark carrying a million connections; see config.SetupPS.
	psSetup, err := config.SetupPS(*framework, *ip, *psMode, time.Millisecond*time.Duration(*psInterval))
	defer psSetup.Source.Stop()
	serverPid, pprofAddr := psSetup.ServerPid, psSetup.PprofAddr
	if err != nil {
		logging.Printf("SetupPS(%v) failed: %v", *framework, err)
	}
	if pprofAddr != "" {
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
	be.PsSource = psSetup.Source
	be.Concurrency = *echoConcurrency
	be.Payload = *payload
	be.Total = *echoTimes
	be.Limit = *echoTPSLimit
	be.EnalbeTPN = *enableTPN
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
	saveReport(beReport)
	logging.Print(logging.ShortLine)
	logging.Print(beReport.String(*enableTPN))
	logging.Print("\n")
	logging.Print(logging.ShortLine)

	if *rateEnabled {
		br := benchrate.New(*framework, serverPid, *ip, cs.Options, cs.NBConns(), *checkValid)
		br.PsSource = psSetup.Source
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
		saveReport(brReport)
		logging.Print(logging.ShortLine)
		logging.Print(brReport.String(*enableTPN))
		logging.Print("\n")
		logging.Print(logging.ShortLine)
	}
}

// saveReport writes one report and says so when it cannot. The error used to
// be dropped, which is how a row could go missing from the report files
// without a word in the log.
func saveReport(r report.Report) {
	if err := report.ToFile(r, *preffix, *suffix); err != nil {
		logging.Printf("%v: writing the %v report failed: %v", r.Name(), r.Type(), err)
	}
}

// generateReports writes the Summary table and the three report tables, each
// to its own .md file and to the console, where each one gets a rule above it
// and its name, and a blank line on either side of its table.
// benchcli-uwscpp's generateReports prints the same thing.
func generateReports() {
	sections := []struct{ name, data string }{
		{"Summary", report.GenerateSummary(*preffix, *suffix)},
		{"Connections", report.GenerateConnectionsReports(*preffix, *suffix, *enableTPN, *reportSort, nil)},
		{"BenchEcho", report.GenerateBenchEchoReports(*preffix, *suffix, *enableTPN, *reportSort, nil)},
		{"BenchRate", report.GenerateBenchRateReports(*preffix, *suffix, *enableTPN, *reportSort, nil)},
	}
	for _, section := range sections {
		filename := report.Filename(section.name, *preffix, *suffix+".md")
		if err := report.WriteFile(filename, section.data); err != nil {
			logging.Printf("writing %v failed: %v", filename, err)
		}
		logging.Print(report.ConsoleSection(*preffix+section.name+*suffix, section.data))
	}
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
