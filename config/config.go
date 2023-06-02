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
	FasthttpWS         = "fasthttp_ws"
	Gobwas             = "gobwas"
	Gorilla            = "gorilla"
	Gws                = "gws"
	GwsBasedonStdhttp  = "gws_basedon_stdhttp"
	Hertz              = "hertz"
	NbioBasedonStdhttp = "nbio_basedon_stdhttp"
	NbioModBlocking    = "nbio_mod_blocking"
	NbioModMixed       = "nbio_mod_mixed"
	NbioModNonblocking = "nbio_mod_nonblocking"
	Nhooyr             = "nhooyr"
)

var Ports = map[string]string{
	FasthttpWS:         "10001:10050",
	Gobwas:             "11001:11050",
	Gorilla:            "12001:12050",
	Gws:                "13001:13050",
	GwsBasedonStdhttp:  "14001:14050",
	Hertz:              "15001:15050",
	NbioBasedonStdhttp: "16001:16050",
	NbioModBlocking:    "17001:17050",
	NbioModMixed:       "18001:18050",
	NbioModNonblocking: "19001:19050",
	Nhooyr:             "20001:20050",
}

var FrameworkList = []string{
	FasthttpWS,
	Gobwas,
	Gorilla,
	Gws,
	GwsBasedonStdhttp,
	Hertz,
	NbioBasedonStdhttp,
	NbioModBlocking,
	NbioModMixed,
	NbioModNonblocking,
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
