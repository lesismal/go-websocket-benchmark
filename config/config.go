package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"go-websocket-benchmark/logging"

	"github.com/lesismal/perf"
)

type InitArgs struct {
	PsInterval time.Duration
}

// Every framework this benchmark knows, by the name that -f, the server binary
// and the report row all take.
//
// This list, Ports and FrameworkList below are kept in framework-name order,
// as are the framework lists in script/config.sh and
// script/1m_conns_benchmark.sh, so that a framework sits in the same place in
// all of them and a new one has one obvious place to go in each.
//
// A name ending in InlineSuffix is not a framework of its own but the Go event
// loop server it is named after, run with -taskpool=inline: see Inlines.
const (
	Fasthttp           = "fasthttp"
	Fib                = "fib"
	FibInline          = "fib-inline"
	Fnet               = "fnet"
	FnetInline         = "fnet-inline"
	Gobwas             = "gobwas"
	Gorilla            = "gorilla"
	Greatws            = "greatws"
	GreatwsInline      = "greatws-inline"
	GreatwsEvent       = "greatws_event"
	GreatwsEventInline = "greatws_event-inline"
	Gws                = "gws"
	GwsStd             = "gws_std"
	Hertz              = "hertz"
	HertzStd           = "hertz_std"
	NbioModBlocking    = "nbio_blocking"
	NbioModMixed       = "nbio_mixed"
	NbioModMixedInline = "nbio_mixed-inline"
	NbioModNonblocking = "nbio_nonblocking"
	NbioStd            = "nbio_std"
	GoNettyWs          = "nettyws"
	Nhooyr             = "nhooyr"
	Quickws            = "quickws"
	TokioTungstenite   = "tokio_tungstenite"
	Uwebsockets        = "uwebsockets"
	UwsEvents          = "uws_events"
	UwsStdio           = "uws_std"
)

// InlineSuffix turns a framework's name into the name of its inline entry.
const InlineSuffix = "-inline"

// Inlines maps the event loop servers that take a pool to their inline entry:
// the same server binary, run so that the callback answers on the poller that
// read the frame, on ports of its own so that the two can be up at once. The
// framework's own entry runs off the event loop, so that every one of these
// frameworks is measured both ways in one run.
//
// For the Go servers that is -taskpool=inline, from which the server takes the
// entry's name, and so its ports: see frameworks.Name. Their own entry runs
// whichever pool script/config.sh selects, which can no longer be inline.
//
// uws_std is not here, since it reads on a goroutine per connection rather
// than in an event loop, and neither is uws_events: UIO runs every
// connection's callbacks in a task off its event loops, and a UIO executor
// must not run that task inline.
var Inlines = map[string]string{
	Fib:          FibInline,
	Fnet:         FnetInline,
	Greatws:      GreatwsInline,
	GreatwsEvent: GreatwsEventInline,
	NbioModMixed: NbioModMixedInline,
}

// An inline entry's ports are its framework's, 100 up: x101 to x150, and the
// control port after them where the framework has one of its own.
var Ports = map[string]string{
	Fasthttp:           "10001:10050",
	Fib:                "29001:29050",
	FibInline:          "29101:29150",
	Fnet:               "30001:30050",
	FnetInline:         "30101:30150",
	Gobwas:             "11001:11050",
	Gorilla:            "12001:12050",
	Greatws:            "24001:24050",
	GreatwsInline:      "24101:24150",
	GreatwsEvent:       "25001:25050",
	GreatwsEventInline: "25101:25150",
	Gws:                "13001:13050",
	GwsStd:             "14001:14050",
	Hertz:              "15001:15050",
	HertzStd:           "16001:16050",
	NbioModBlocking:    "17001:17050",
	NbioModMixed:       "18001:18050",
	NbioModMixedInline: "18101:18150",
	NbioModNonblocking: "19001:19050",
	NbioStd:            "20001:20050",
	GoNettyWs:          "21001:21050",
	Nhooyr:             "22001:22050",
	Quickws:            "23001:23050",
	TokioTungstenite:   "32001:32050",
	Uwebsockets:        "31001:31050",
	UwsEvents:          "28001:28050",
	UwsStdio:           "26001:26050",
}

// The languages a framework's server is written in, as the reports' Lang
// column shows them.
const (
	LangCPP  = "c++"
	LangGo   = "go"
	LangRust = "rust"
)

// Langs is the language of every framework's server, in framework-name order
// like Ports.
var Langs = map[string]string{
	Fasthttp:           LangGo,
	Fib:                LangGo,
	FibInline:          LangGo,
	Fnet:               LangGo,
	FnetInline:         LangGo,
	Gobwas:             LangGo,
	Gorilla:            LangGo,
	Greatws:            LangGo,
	GreatwsInline:      LangGo,
	GreatwsEvent:       LangGo,
	GreatwsEventInline: LangGo,
	Gws:                LangGo,
	GwsStd:             LangGo,
	Hertz:              LangGo,
	HertzStd:           LangGo,
	NbioModBlocking:    LangGo,
	NbioModMixed:       LangGo,
	NbioModMixedInline: LangGo,
	NbioModNonblocking: LangGo,
	NbioStd:            LangGo,
	GoNettyWs:          LangGo,
	Nhooyr:             LangGo,
	Quickws:            LangGo,
	TokioTungstenite:   LangRust,
	Uwebsockets:        LangCPP,
	UwsEvents:          LangGo,
	UwsStdio:           LangGo,
}

// FrameworkLang is the language framework's server is written in, for the
// reports' Lang column, or "-" for a framework it does not know.
// benchcli-uwscpp reads the same table, through
// benchcli-uwscpp/generate_metadata.py.
func FrameworkLang(framework string) string {
	if lang, ok := Langs[framework]; ok {
		return lang
	}
	return "-"
}

// FrameworkServesPprof reports whether framework's server has the
// /debug/pprof routes a client can fetch a profile from. Only the Go servers
// do - they get them from net/http/pprof, through frameworks.HandleCommon -
// so the clients skip the profile for every other one rather than ask for it
// and log a 404. benchcli-uwscpp and benchcli-rust decide it the same way,
// from the same Langs table.
func FrameworkServesPprof(framework string) bool {
	return FrameworkLang(framework) == LangGo
}

// FrameworkList is every framework, in framework-name order. It is also the
// row order of a -sort=framework report, which is what puts a framework on the
// same row in every table and across runs, whatever it scored.
var FrameworkList = []string{
	Fasthttp,
	Fib,
	FibInline,
	Fnet,
	FnetInline,
	Gobwas,
	Gorilla,
	Greatws,
	GreatwsInline,
	GreatwsEvent,
	GreatwsEventInline,
	Gws,
	GwsStd,
	Hertz,
	HertzStd,
	NbioModBlocking,
	NbioModMixed,
	NbioModMixedInline,
	NbioModNonblocking,
	NbioStd,
	GoNettyWs,
	Nhooyr,
	Quickws,
	TokioTungstenite,
	Uwebsockets,
	UwsEvents,
	UwsStdio,
}

func GetFrameworkBenchmarkPorts(framework string) ([]int, error) {
	portRange := strings.Split(Ports[framework], ":")
	minPort, err := strconv.Atoi(portRange[0])
	if err != nil {
		return nil, err
	}
	maxPort, err := strconv.Atoi(portRange[1])
	if err != nil {
		return nil, err
	}
	ports := []int{}
	for i := minPort; i <= maxPort; i++ {
		ports = append(ports, i)
	}
	return ports, nil
}

func GetFrameworkServerAddrs(framework string) ([]string, error) {
	ports, err := GetFrameworkBenchmarkPorts(framework)
	if err != nil {
		return nil, err
	}
	addrs := make([]string, 0, len(ports))
	for _, port := range ports {
		addrs = append(addrs, fmt.Sprintf(":%d", port))
	}
	return addrs, nil
}

func GetFrameworkHTTPServerAddrs(framework string) (string, error) {
	ports, err := GetFrameworkBenchmarkPorts(framework)
	if err != nil {
		return "", err
	}
	addr := fmt.Sprintf(":%d", ports[len(ports)-1]+1)
	return addr, nil
}

// urlHost brackets a bare IPv6 literal so that it can carry a port in a URL,
// the way benchcli-uwscpp's controlURL does. BENCH_SERVER_HOST may be an
// address or a hostname, and an IPv6 address without this comes out as
// ws://fe80::1:12001/ws, which parses as neither host nor port.
func urlHost(ip string) string {
	if strings.Contains(ip, ":") && !strings.HasPrefix(ip, "[") {
		return "[" + ip + "]"
	}
	return ip
}

func GetFrameworkBenchmarkAddrs(framework, ip string) ([]string, error) {
	ports, err := GetFrameworkBenchmarkPorts(framework)
	if err != nil {
		return nil, err
	}
	addrs := make([]string, 0, len(ports))
	for _, port := range ports {
		addrs = append(addrs, fmt.Sprintf("ws://%s:%d/ws", urlHost(ip), port))
	}
	return addrs, nil
}

// Control requests - /init, /ps, /taskpool - go to the pid port, which for
// most frameworks is also carrying benchmark connections. At a hundred
// thousand of them one attempt is not enough: a server still working through
// the backlog of a just-finished rate test can reset the connection or sit on
// the request past any sane deadline, and the resource columns that silently
// read 0 when that happened took EER down with them. So retry, patiently, and
// say what failed when it still does.
const (
	controlAttempts = 4
	controlTimeout  = 30 * time.Second
	controlBackoff  = 2 * time.Second
)

// One client for every control request, so a retry can reuse a connection the
// server has already accepted.
var controlClient = &http.Client{Timeout: controlTimeout}

// controlRequest sends one control request, retrying a transport failure up to
// attempts times. A reply the server actually produced is returned as it is,
// including a 404: the route is not there and waiting will not put it there,
// which is what hertz and hertz_std do with /taskpool.
func controlRequest(url string, body []byte, attempts int) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Duration(attempt-1) * controlBackoff)
		}
		data, answered, err := controlOnce(url, body)
		if err == nil {
			return data, nil
		}
		lastErr = fmt.Errorf("%v: %w", url, err)
		if answered {
			break
		}
		if attempt < attempts {
			logging.Printf("control request failed, retrying (%d/%d): %v", attempt, attempts, lastErr)
		}
	}
	return nil, lastErr
}

// controlOnce reports whether the server answered at all, so that the caller
// can tell a route that is missing from a server that is too busy to reply.
func controlOnce(url string, body []byte) (data []byte, answered bool, err error) {
	var res *http.Response
	if body == nil {
		res, err = controlClient.Get(url)
	} else {
		res, err = controlClient.Post(url, "", bytes.NewReader(body))
	}
	if err != nil {
		return nil, false, err
	}
	defer res.Body.Close()
	data, err = io.ReadAll(res.Body)
	if err != nil {
		return nil, false, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, true, fmt.Errorf("%v: %s", res.Status, bytes.TrimSpace(data))
	}
	return data, true, nil
}

// frameworkControlPort is the port a framework's control routes - /init, /ps,
// /taskpool and the pprof ones - listen on. For most frameworks it is also
// the last of the ports carrying benchmark connections; the ones that serve
// their control routes separately take the one after it.
func frameworkControlPort(framework string) (int, error) {
	ports, err := GetFrameworkBenchmarkPorts(framework)
	if err != nil {
		return 0, err
	}
	port := ports[len(ports)-1]
	switch framework {
	case Fib, FibInline, Gws, UwsEvents, UwsStdio:
		port++
	}
	return port, nil
}

// FrameworkControlAddr is the base URL of those routes. A client can work it
// out on its own, without the request to /init that used to be the only way
// it learned where to fetch a pprof profile from.
func FrameworkControlAddr(framework, ip string) (string, error) {
	port, err := frameworkControlPort(framework)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%v:%v", urlHost(ip), port), nil
}

func InitAndGetFrameworkPid(framework, ip string, args *InitArgs) (int, string, error) {
	pprofAddr, err := FrameworkControlAddr(framework, ip)
	if err != nil {
		return -1, "", err
	}
	serverAddr := pprofAddr + "/init"

	data, _ := json.Marshal(args)
	// A failed /init is not just a missing pid: it is a server that never
	// started sampling, so every CPU and MEM column of the run would be 0.
	body, err := controlRequest(serverAddr, data, controlAttempts)
	if err != nil {
		return -1, "", err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))

	return pid, pprofAddr, err
}

// GetFrameworkPsInfo reads the server's CPU and memory samples. It returns
// the counter it managed to read alongside an error as well as instead of
// one, so that samples which did arrive are still reported: an error here
// means the resource columns are incomplete, not that they are all missing.
func GetFrameworkPsInfo(framework, ip string) (*perf.PSCounter, error) {
	controlAddr, err := FrameworkControlAddr(framework, ip)
	if err != nil {
		return nil, err
	}
	serverAddr := controlAddr + "/ps"

	body, err := controlRequest(serverAddr, nil, controlAttempts)
	if err != nil {
		return nil, err
	}

	psCounter := &perf.PSCounter{}
	err = json.Unmarshal(body, psCounter)
	if err != nil {
		return nil, fmt.Errorf("%v: %w", serverAddr, err)
	}
	if psCounter.CPUAvg() <= 0 {
		// The request went through, so the sampler is what did not: either
		// /init never reached this server, or nothing has been sampled yet
		// because the phase was shorter than one -pi interval. Say so rather
		// than letting the columns quietly read 0.
		return psCounter, fmt.Errorf("%v: answered with no CPU samples, so either /init did not"+
			" reach it or the phase was shorter than the -pi sampling interval", serverAddr)
	}

	return psCounter, nil
}

// TaskPoolFrameworks are the frameworks whose servers take the -taskpool
// flags and serve /taskpool with the pool they installed, in framework-name
// order. script/config.sh's taskpool_frameworks is the same list, which
// benchcli-uwscpp/test_scripts.py checks, and benchcli-uwscpp and benchcli-rust
// read this one through benchcli-uwscpp/generate_metadata.py.
var TaskPoolFrameworks = []string{
	Fib,
	FibInline,
	Fnet,
	FnetInline,
	Greatws,
	GreatwsInline,
	GreatwsEvent,
	GreatwsEventInline,
	NbioModMixed,
	NbioModMixedInline,
	NbioModNonblocking,
	TokioTungstenite,
	Uwebsockets,
	UwsEvents,
}

// FrameworkHasTaskPool reports whether framework's server takes a pool, and so
// whether there is a /taskpool to ask. Every other server's Pool is
// TaskPoolNone without a request: nothing it could answer would change that,
// and a request to a server that has already gone - killed for memory, say -
// only adds a connection refused to the log of whatever did kill it.
func FrameworkHasTaskPool(framework string) bool {
	return slices.Contains(TaskPoolFrameworks, framework)
}

// TaskPoolNone is the Pool of a report whose server installed no pool:
// the frameworks that take no -taskpool flag at all, and any server whose
// /taskpool route did not answer.
const TaskPoolNone = "-"

// GetFrameworkTaskPool reports the pool the framework's server is running, by
// the name -taskpool takes, for the reports' Pool. Both clients
// read it once, when they build the report, so that a report says which
// scheduling produced it rather than which one the run asked for - the two
// differ for a server whose own scheduling is one of the pools.
func GetFrameworkTaskPool(framework, ip string) string {
	if !FrameworkHasTaskPool(framework) {
		return TaskPoolNone
	}
	controlAddr, err := FrameworkControlAddr(framework, ip)
	if err != nil {
		return TaskPoolNone
	}
	serverAddr := controlAddr + "/taskpool"

	// hertz and hertz_std serve control routes of their own and have no pool
	// hook, so there is no /taskpool there to answer; controlRequest does not
	// retry a reply the server produced, so that costs no waiting. Fewer
	// attempts than the routes the numbers depend on: this column is worth a
	// second try, not a third and a fourth of a server that is not answering.
	body, err := controlRequest(serverAddr, nil, 2)
	if err != nil {
		return TaskPoolNone
	}
	if name := strings.TrimSpace(string(body)); name != "" {
		return name
	}
	return TaskPoolNone
}
