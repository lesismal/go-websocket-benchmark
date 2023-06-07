package report

import (
	"fmt"
)

var (
	BenchRateReportMarkdownHeaders = []string{}
)

type BenchRateReport struct {
	Framework   string  `json:"Framework" md:"Framework"`
	Used        int64   `json:"Used" md:"Used" fmt:"duration"`
	Total       int     `json:"Total" md:"Total"`
	Success     int64   `json:"Success" md:"Success"`
	Failed      int64   `json:"Failed" md:"Failed"`
	SendTimes   int64   `json:"TPS" md:"TPS"`
	SendBytes   int64   `json:"SendBytes" md:"SendBytes" fmt:"flow"`
	RecvTimes   int64   `json:"RecvTimes" md:"RecvTimes"`
	RecvBytes   int64   `json:"RecvBytes" md:"RecvBytes" fmt:"flow"`
	Connections int     `json:"Conns" md:"Conns"`
	Concurrency int     `json:"Concurrency" md:"Concurrency"`
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
