package config

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/lesismal/nbio/nbhttp/websocket"
)

const (
	Fasthttp           = "fasthttp"
	Gobwas             = "gobwas"
	Gorilla            = "gorilla"
	Gws                = "gws"
	GwsStd             = "gws_std"
	Hertz              = "hertz"
	NbioStd            = "nbio_std"
	NbioModBlocking    = "nbio_blocking"
	NbioModMixed       = "nbio_mixed"
	NbioModNonblocking = "nbio_nonblocking"
	GoNettyWs          = "nettyws"
	Nhooyr             = "nhooyr"
)

var Ports = map[string]string{
	Fasthttp:           "10001:10050",
	Gobwas:             "11001:11050",
	Gorilla:            "13001:13050",
	Gws:                "14001:14050",
	GwsStd:             "15001:15050",
	Hertz:              "16001:16050",
	NbioStd:            "17001:17050",
	NbioModBlocking:    "18001:18050",
	NbioModMixed:       "19001:19050",
	NbioModNonblocking: "20001:20050",
	GoNettyWs:          "12001:12050",
	Nhooyr:             "21001:21050",
}

var FrameworkList = []string{
	Fasthttp,
	Gobwas,
	Gorilla,
	Gws,
	GwsStd,
	Hertz,
	NbioStd,
	NbioModBlocking,
	NbioModMixed,
	NbioModNonblocking,
	GoNettyWs,
	Nhooyr,
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

func GetFrameworkPidServerAddrs(framework string) (string, error) {
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

func GetFrameworkPid(framework, ip string) (int, error) {
	ports, err := GetFrameworkBenchmarkPorts(framework)
	if err != nil {
		return -1, err
	}
	pidPort := ports[len(ports)-1]
	if framework == Gws {
		pidPort++
	}
	serverAddr := fmt.Sprintf("http://%v:%v/pid", ip, pidPort)

	res, err := http.Get(serverAddr)
	if err != nil {
		return -1, err
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return -1, err
	}
	pid, err := strconv.Atoi(string(body))
	return pid, err
}

type EchoSession struct {
	MT    websocket.MessageType
	Bytes []byte
}
