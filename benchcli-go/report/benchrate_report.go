package report

import (
	"fmt"
)

var (
	BenchRateReportMarkdownHeaders = []string{}
)

// BenchRateReport is ranked by Packet Recv (rank:"1"), the messages the clients
// read back off the server: the rate benchmark writes at a rate the clients set
// rather than to completion, so what the server answered under that load is
// its result, the way TPS is in the other two. Rows with the same Packet Recv
// are ranked by EER (rank:"2"), the one that spent less CPU on it first.
type BenchRateReport struct {
	Framework   string  `json:"Framework" md:"Framework"`
	BenchClient string  `json:"BenchClient" md:"Client" fmt:"client" summary:"Client"`
	TaskPool    string  `json:"TaskPool" md:"Pool" summary:"Pool"`
	Duration    int64   `json:"Duration" md:"Duration" fmt:"duration" summary:"Rate Duration"`
	EchoEER     float64 `json:"EchoEER" md:"EER" rank:"2"`
	SendTimes   int64   `json:"SendTimes" md:"Packet Sent"`
	SendBytes   int64   `json:"SendBytes" md:"Bytes Sent" fmt:"mem"`
	RecvTimes   int64   `json:"RecvTimes" md:"Packet Recv" rank:"1"`
	RecvBytes   int64   `json:"RecvBytes" md:"Bytes Recv" fmt:"mem"`
	Connections int     `json:"Conns" md:"Conns" summary:"Conns"`
	Concurrency int     `json:"Concurrency" md:"Concurrency" summary:"Rate Concurrency"`
	SendRate    int     `json:"SendRate" md:"SendRate" summary:"Rate SendRate"`
	Payload     int     `json:"Payload" md:"Payload" summary:"Payload"`
	// GoMin       int     `json:"GoMin" md:"Go Min" fmt:"go"`
	// GoAvg       int     `json:"GoAvg" md:"Go Avg" fmt:"go"`
	// GoMax       int     `json:"GoMax" md:"Go Max" fmt:"go"`
	CPUMin       float64 `json:"CPUMin" md:"-" fmt:"cpu"`
	CPUAvg       float64 `json:"CPUAvg" md:"CPU Avg" fmt:"cpu"`
	CPUMax       float64 `json:"CPUMax" md:"CPU Max" fmt:"cpu"`
	MEMRSSMin    uint64  `json:"MEMMin" md:"-" fmt:"mem"`
	MEMRSSAvg    uint64  `json:"MEMAvg" md:"MEM Avg" fmt:"mem"`
	MEMRSSMax    uint64  `json:"MEMMax" md:"MEM Max" fmt:"mem"`
	pprofDataCPU []byte  `json:"-" md:"-" fmt:"-"`
	pprofDataMEM []byte  `json:"-" md:"-" fmt:"-"`
}

func (r *BenchRateReport) Type() string {
	return "BenchRate"
}

func (r *BenchRateReport) Name() string {
	return fmt.Sprintf("%s-BenchRate", r.Framework)
}

func (r *BenchRateReport) Headers() []string {
	return BenchRateReportMarkdownHeaders
}

func (r *BenchRateReport) Fields(enableTPN bool) []string {
	return ObjFieldValues(r, enableTPN)
}

func (r *BenchRateReport) SetPprofData(cpu, mem []byte) {
	r.pprofDataCPU = cpu
	r.pprofDataMEM = mem
}

func (r *BenchRateReport) PprofCPU() []byte {
	return r.pprofDataCPU
}

func (r *BenchRateReport) PprofMEM() []byte {
	return r.pprofDataMEM
}

func (r *BenchRateReport) String(enableTPN bool) string {
	return ObjString(r, enableTPN)
}
