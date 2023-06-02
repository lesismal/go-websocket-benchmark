package report

import "fmt"

var (
	benchEchoReportMarkdownHeaders = []string{}
)

type BenchEchoReport struct {
	Framework   string  `json:"Framework" md:"Framework"`
	Connections int     `json:"Conns" md:"Conns"`
	Concurrency int     `json:"Concurrency" md:"Concurrency"`
	Payload     int     `json:"Payload" md:"Payload"`
	Total       int     `json:"Total" md:"Total"`
	Success     int64   `json:"Success" md:"Success"`
	Failed      int64   `json:"Failed" md:"Failed"`
	Used        int64   `json:"Used" md:"Used" fmt:"duration"`
	CPUMin      float64 `json:"CPUMin" md:"CPU Min" fmt:"cpu"`
	CPUAvg      float64 `json:"CPUAvg" md:"CPU Avg" fmt:"cpu"`
	CPUMax      float64 `json:"CPUMax" md:"CPU Max" fmt:"cpu"`
	MEMRSSMin   uint64  `json:"MEMMin" md:"MEM Min" fmt:"mem"`
	MEMRSSAvg   uint64  `json:"MEMAvg" md:"MEM Avg" fmt:"mem"`
	MEMRSSMax   uint64  `json:"MEMMax" md:"MEM Max" fmt:"mem"`
	TPS         int64   `json:"TPS" md:"TPS"`
	Min         int64   `json:"Min" md:"Min" fmt:"duration"`
	Avg         int64   `json:"Avg" md:"Avg" fmt:"duration"`
	Max         int64   `json:"Max" md:"Max" fmt:"duration"`
	TP50        int64   `json:"TP50" md:"TP50" fmt:"duration"`
	TP75        int64   `json:"TP75" md:"TP75" fmt:"duration"`
	TP90        int64   `json:"TP90" md:"TP90" fmt:"duration"`
	TP95        int64   `json:"TP95" md:"TP95" fmt:"duration"`
	TP99        int64   `json:"TP99" md:"TP99" fmt:"duration"`
}

func (r *BenchEchoReport) Name() string {
	return fmt.Sprintf("%s-BenchEcho", r.Framework)
}

func (r *BenchEchoReport) Headers() []string {
	return benchEchoReportMarkdownHeaders
}

func (r *BenchEchoReport) Fields() []string {
	return ObjFieldValues(r)
}

func (r *BenchEchoReport) String() string {
	return ObjString(r)
}
