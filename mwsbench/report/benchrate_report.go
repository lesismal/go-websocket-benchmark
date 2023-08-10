package report

import (
	"fmt"
)

var (
	BenchRateReportMarkdownHeaders = []string{}
)

type BenchRateReport struct {
	Framework   string  `json:"Framework" md:"Framework"`
	EchoEER     float64 `json:"EchoEER" md:"EchoEER"`
	Duration    int64   `json:"Duration" md:"Duration" fmt:"duration"`
	SendTimes   int64   `json:"SendTimes" md:"Packet Sent"`
	SendBytes   int64   `json:"SendBytes" md:"Bytes Sent" fmt:"mem"`
	RecvTimes   int64   `json:"RecvTimes" md:"Packet Recv"`
	RecvBytes   int64   `json:"RecvBytes" md:"Bytes Recv" fmt:"mem"`
	Connections int     `json:"Conns" md:"Conns"`
	SendRate    int     `json:"SendRate" md:"SendRate"`
	Payload     int     `json:"Payload" md:"Payload"`
	CPUMin      float64 `json:"CPUMin" md:"CPU Min" fmt:"cpu"`
	CPUAvg      float64 `json:"CPUAvg" md:"CPU Avg" fmt:"cpu"`
	CPUMax      float64 `json:"CPUMax" md:"CPU Max" fmt:"cpu"`
	MEMRSSMin   uint64  `json:"MEMMin" md:"MEM Min" fmt:"mem"`
	MEMRSSAvg   uint64  `json:"MEMAvg" md:"MEM Avg" fmt:"mem"`
	MEMRSSMax   uint64  `json:"MEMMax" md:"MEM Max" fmt:"mem"`
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

func (r *BenchRateReport) Fields() []string {
	return ObjFieldValues(r)
}

func (r *BenchRateReport) String() string {
	return ObjString(r)
}
