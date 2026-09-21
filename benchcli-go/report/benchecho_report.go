package report

import (
	"fmt"

	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	BenchEchoReportMarkdownHeaders = []string{}
)

type EchoSession struct {
	MT    websocket.MessageType
	Bytes []byte
}

type BenchEchoReport struct {
	Framework   string  `json:"Framework" md:"Framework"`
	TPS         int64   `json:"TPS" md:"TPS"`
	EER         float64 `json:"EER" md:"EER"`
	Min         int64   `json:"Min" md:"Min" fmt:"duration" tpn:"opt"`
	Avg         int64   `json:"Avg" md:"Avg" fmt:"duration" tpn:"opt"`
	Max         int64   `json:"Max" md:"Max" fmt:"duration" tpn:"opt"`
	TP50        int64   `json:"TP50" md:"TP50" fmt:"duration" tpn:"opt"`
	TP75        int64   `json:"TP75" md:"TP75" fmt:"duration" tpn:"opt"`
	TP90        int64   `json:"TP90" md:"TP90" fmt:"duration" tpn:"opt"`
	TP95        int64   `json:"TP95" md:"TP95" fmt:"duration" tpn:"opt"`
	TP99        int64   `json:"TP99" md:"TP99" fmt:"duration" tpn:"opt"`
	Used        int64   `json:"Used" md:"Used" fmt:"duration"`
	Total       int     `json:"Total" md:"Total"`
	Success     int64   `json:"Success" md:"Success"`
	Failed      int64   `json:"Failed" md:"Failed"`
	Connections int     `json:"Conns" md:"Conns"`
	Concurrency int     `json:"Concurrency" md:"Concurrency"`
	Payload     int     `json:"Payload" md:"Payload"`
	// GoMin       int     `json:"GoMin" md:"Go Min" fmt:"go"`
	// GoAvg       int     `json:"GoAvg" md:"Go Avg" fmt:"go"`
	// GoMax       int     `json:"GoMax" md:"Go Max" fmt:"go"`
	CPUMin       float64 `json:"CPUMin" md:"CPU Min" fmt:"cpu"`
	CPUAvg       float64 `json:"CPUAvg" md:"CPU Avg" fmt:"cpu"`
	CPUMax       float64 `json:"CPUMax" md:"CPU Max" fmt:"cpu"`
	MEMRSSMin    uint64  `json:"MEMMin" md:"MEM Min" fmt:"mem"`
	MEMRSSAvg    uint64  `json:"MEMAvg" md:"MEM Avg" fmt:"mem"`
	MEMRSSMax    uint64  `json:"MEMMax" md:"MEM Max" fmt:"mem"`
	pprofDataCPU []byte  `json:"-" md:"-" fmt:"-"`
	pprofDataMEM []byte  `json:"-" md:"-" fmt:"-"`
}

func (r *BenchEchoReport) Type() string {
	return "BenchEcho"
}

func (r *BenchEchoReport) Name() string {
	return fmt.Sprintf("%s-BenchEcho", r.Framework)
}

func (r *BenchEchoReport) Headers() []string {
	return BenchEchoReportMarkdownHeaders
}

func (r *BenchEchoReport) Fields(enableTPN bool) []string {
	return ObjFieldValues(r, enableTPN)
}

func (r *BenchEchoReport) PprofCPU() []byte {
	return r.pprofDataCPU
}

func (r *BenchEchoReport) PprofMEM() []byte {
	return r.pprofDataMEM
}

func (r *BenchEchoReport) SetPprofData(cpu, mem []byte) {
	r.pprofDataCPU = cpu
	r.pprofDataMEM = mem
}

func (r *BenchEchoReport) String(enableTPN bool) string {
	return ObjString(r, enableTPN)
}
