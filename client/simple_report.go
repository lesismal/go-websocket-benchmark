package main

import (
	"fmt"
	"time"

	"github.com/lesismal/perf"
)

type SimpleReport struct {
	Framework string
	Total     int64
	Success   int64
	Failed    int64
	TimeUsed  time.Duration
	Min       int64 `json:"-"`
	Avg       int64
	Max       int64 `json:"-"`
	TPS       int64
	TP50      int64
	TP75      int64 `json:"-"`
	TP90      int64
	TP95      int64 `json:"-"`
	TP99      int64
	CPUMin    float64 `json:"-"`
	CPUAvg    float64
	CPUMax    float64 `json:"-"`
	MEMRSSMin uint64  `json:"-"`
	MEMRSSAvg uint64
	MEMRSSMax uint64 `json:"-"`
}

func (r *SimpleReport) Headers() []string {
	ret := [12]string{
		"Framework",
		"Total",
		"Success",
		"Failed",
		"Used",
		// "Min",
		"Avg",
		// "Max",
		"TPS",
		"TP50",
		// "TP75",
		"TP90",
		// "TP95",
		"TP99",
		// "CPU Min",
		"CPU Avg",
		// "CPU Max",
		// "MEM Min",
		"MEM Avg",
		// "MEM Max",
	}
	return ret[:]
}

func (r *SimpleReport) Strings() []string {
	ret := make([]string, 20)[:0]
	ret = append(ret, r.Framework)
	ret = append(ret, fmt.Sprintf("%d", r.Total))
	ret = append(ret, fmt.Sprintf("%v", r.Success))
	ret = append(ret, fmt.Sprintf("%v", r.Failed))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(int64(r.TimeUsed))))
	// ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.Min)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.Avg)))
	// ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.Max)))
	ret = append(ret, fmt.Sprintf("%v", r.TPS))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.TP50)))
	// ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.TP75)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.TP90)))
	// ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.TP95)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2TimeString(r.TP99)))
	// ret = append(ret, fmt.Sprintf("%.2f%%", r.CPUMin))
	ret = append(ret, fmt.Sprintf("%.2f%%", r.CPUAvg))
	// ret = append(ret, fmt.Sprintf("%.2f%%", r.CPUMax))
	// ret = append(ret, fmt.Sprintf("%v", perf.I2MemString(r.MEMRSSMin)))
	ret = append(ret, fmt.Sprintf("%v", perf.I2MemString(r.MEMRSSAvg)))
	// ret = append(ret, fmt.Sprintf("%v", perf.I2MemString(r.MEMRSSMax)))
	return ret
}
