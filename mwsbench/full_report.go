package main

import (
	"fmt"
	"time"
	"unsafe"

	"github.com/lesismal/perf"
)

type FullReport struct {
	Framework        string
	Connections      int
	DialConcurrency  int
	BenchConcurrency int
	ConnectSuccess   int
	ConnectFailed    int
	ConnectUsed      time.Duration
	Payload          int
	Total            int64
	Success          int64
	Failed           int64
	TimeUsed         time.Duration
	Min              int64
	Avg              int64
	Max              int64
	TPS              int64
	TP50             int64
	TP75             int64
	TP90             int64
	TP95             int64
	TP99             int64
	CPUMin           float64
	CPUAvg           float64
	CPUMax           float64
	MEMRSSMin        uint64
	MEMRSSAvg        uint64
	MEMRSSMax        uint64
}

func (r *FullReport) Headers() []string {
	ret := [22]string{
		"Framework",
		"Conns",
		"Payload",
		"Total",
		"Success",
		"Failed",
		"Used",
		"CPU Min",
		"CPU Avg",
		"CPU Max",
		"MEM Min",
		"MEM Avg",
		"MEM Max",
		"Min",
		"Avg",
		"Max",
		"TPS",
		"TP50",
		"TP75",
		"TP90",
		"TP95",
		"TP99",
	}
	return ret[:]
}

func (r *FullReport) Strings() []string {
	ret := make([]string, 20)[:0]
	ret = append(ret, r.Framework)
	ret = append(ret, fmt.Sprintf("%d", r.Connections))
	ret = append(ret, fmt.Sprintf("%d", r.Payload))
	ret = append(ret, fmt.Sprintf("%d", r.Total))
	ret = append(ret, fmt.Sprintf("%v", r.Success))
	ret = append(ret, fmt.Sprintf("%v", r.Failed))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(int64(r.TimeUsed))))
	ret = append(ret, fmt.Sprintf("%.2f%%", r.CPUMin))
	ret = append(ret, fmt.Sprintf("%.2f%%", r.CPUAvg))
	ret = append(ret, fmt.Sprintf("%.2f%%", r.CPUMax))
	ret = append(ret, fmt.Sprintf("%v", perf.I2MemString(r.MEMRSSMin)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2MemString(r.MEMRSSAvg)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2MemString(r.MEMRSSMax)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.Min)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.Avg)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.Max)))
	ret = append(ret, fmt.Sprintf("%v", r.TPS))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.TP50)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.TP75)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.TP90)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.TP95)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.TP99)))

	return ret
}

func (r *FullReport) ToSimple() *SimpleReport {
	sr := &SimpleReport{}
	*sr = *(*SimpleReport)(unsafe.Pointer(r))
	return sr
	// return &SimpleReport{
	// 	Framework:   r.Framework,
	// 	Connections: r.Connections,
	// 	Payload:     r.Payload,
	// 	Total:       r.Total,
	// 	Success:     r.Success,
	// 	Failed:      r.Failed,
	// 	TimeUsed:    r.TimeUsed,
	// 	Min:         r.Min,
	// 	Avg:         r.Avg,
	// 	Max:         r.Max,
	// 	TPS:         r.TPS,
	// 	TP50:        r.TP50,
	// 	TP75:        r.TP75,
	// 	TP90:        r.TP90,
	// 	TP95:        r.TP95,
	// 	TP99:        r.TP99,
	// 	CPUMin:      r.CPUMin,
	// 	CPUAvg:      r.CPUAvg,
	// 	CPUMax:      r.CPUMax,
	// 	MEMRSSMin:   r.MEMRSSMin,
	// 	MEMRSSAvg:   r.MEMRSSAvg,
	// 	MEMRSSMax:   r.MEMRSSMax,
	// }
}
