package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lesismal/perf"
)

type InitArgs struct {
	PsInterval time.Duration
}

const (
	Fasthttp           = "fasthttp"
	Fib                = "fib"
	Gobwas             = "gobwas"
	Gorilla            = "gorilla"
	Gws                = "gws"
	GwsStd             = "gws_std"
	Hertz              = "hertz"
	HertzStd           = "hertz_std"
	NbioModBlocking    = "nbio_blocking"
	NbioModMixed       = "nbio_mixed"
	NbioModNonblocking = "nbio_nonblocking"
	NbioStd            = "nbio_std"
	GoNettyWs          = "nettyws"
	Nhooyr             = "nhooyr"
	Quickws            = "quickws"
	Greatws            = "greatws"
	GreatwsEvent       = "greatws_event"
	UwsStdio           = "uws_std"
	UwsEvents          = "uws_events"
	Fnet               = "fnet"
	Uwebsockets        = "uwebsockets"
)

var Ports = map[string]string{
	Fasthttp:           "10001:10050",
	Fib:                "29001:29050",
	Gobwas:             "11001:11050",
	Gorilla:            "12001:12050",
	Gws:                "13001:13050",
	GwsStd:             "14001:14050",
	Hertz:              "15001:15050",
	HertzStd:           "16001:16050",
	NbioModBlocking:    "17001:17050",
	NbioModMixed:       "18001:18050",
	NbioModNonblocking: "19001:19050",
	NbioStd:            "20001:20050",
	GoNettyWs:          "21001:21050",
	Nhooyr:             "22001:22050",
	Quickws:            "23001:23050",
	Greatws:            "24001:24050",
	GreatwsEvent:       "25001:25050",
	UwsStdio:           "26001:26050",
	UwsEvents:          "28001:28050",
	Fnet:               "30001:30050",
	Uwebsockets:        "31001:31050",
}

var FrameworkList = []string{
	Fasthttp,
	Fib,
	Gobwas,
	Gorilla,
	Gws,
	GwsStd,
	Hertz,
	HertzStd,
	NbioModBlocking,
	NbioModMixed,
	NbioModNonblocking,
	NbioStd,
	GoNettyWs,
	Nhooyr,
	Quickws,
	Greatws,
	GreatwsEvent,
	UwsStdio,
	UwsEvents,
	Fnet,
	Uwebsockets,
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

func GetFrameworkBenchmarkAddrs(framework, ip string) ([]string, error) {
	ports, err := GetFrameworkBenchmarkPorts(framework)
	if err != nil {
		return nil, err
	}
	addrs := make([]string, 0, len(ports))
	for _, port := range ports {
		addrs = append(addrs, fmt.Sprintf("ws://%s:%d/ws", ip, port))
	}
	return addrs, nil
}

func InitAndGetFrameworkPid(framework, ip string, args *InitArgs) (int, string, error) {
	ports, err := GetFrameworkBenchmarkPorts(framework)
	if err != nil {
		return -1, "", err
	}
	pidPort := ports[len(ports)-1]
	if framework == Fib || framework == Gws || framework == UwsStdio || framework == UwsEvents {
		pidPort++
	}
	serverAddr := fmt.Sprintf("http://%v:%v/init", ip, pidPort)

	data, _ := json.Marshal(args)
	res, err := http.Post(serverAddr, "", bytes.NewReader(data))
	if err != nil {
		return -1, "", err
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return -1, "", err
	}
	pid, err := strconv.Atoi(string(body))
	pprofAddr := fmt.Sprintf("http://%v:%v", ip, pidPort)

	return pid, pprofAddr, err
}

func GetFrameworkPsInfo(framework, ip string) (*perf.PSCounter, error) {
	ports, err := GetFrameworkBenchmarkPorts(framework)
	if err != nil {
		return nil, err
	}
	pidPort := ports[len(ports)-1]
	if framework == Fib || framework == Gws || framework == UwsStdio || framework == UwsEvents {
		pidPort++
	}
	serverAddr := fmt.Sprintf("http://%v:%v/ps", ip, pidPort)

	res, err := http.Get(serverAddr)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	psCounter := &perf.PSCounter{}
	err = json.Unmarshal(body, psCounter)
	if err != nil {
		return nil, err
	}

	return psCounter, nil
}

// TaskPoolNone is the Pool column of a report whose server installed no pool:
// the frameworks that take no -taskpool flag at all, and any server whose
// /taskpool route did not answer.
const TaskPoolNone = "-"

// GetFrameworkTaskPool reports the pool the framework's server is running, by
// the name -taskpool takes, for the Pool column of the reports. Both clients
// read it once, when they build the report, so that a report says which
// scheduling produced it rather than which one the run asked for - the two
// differ for a server whose own scheduling is one of the pools.
func GetFrameworkTaskPool(framework, ip string) string {
	ports, err := GetFrameworkBenchmarkPorts(framework)
	if err != nil {
		return TaskPoolNone
	}
	pidPort := ports[len(ports)-1]
	if framework == Fib || framework == Gws || framework == UwsStdio || framework == UwsEvents {
		pidPort++
	}
	serverAddr := fmt.Sprintf("http://%v:%v/taskpool", ip, pidPort)

	res, err := http.Get(serverAddr)
	if err != nil {
		return TaskPoolNone
	}
	defer res.Body.Close()
	// hertz and hertz_std serve control routes of their own and have no pool
	// hook, so there is no /taskpool there to answer.
	if res.StatusCode != http.StatusOK {
		return TaskPoolNone
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return TaskPoolNone
	}
	if name := strings.TrimSpace(string(body)); name != "" {
		return name
	}
	return TaskPoolNone
}
