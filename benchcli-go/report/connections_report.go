package report

import "fmt"

var (
	ConnectionsReportMarkdownHeaders = []string{}
)

type ConnectionsReport struct {
	Framework   string `json:"Framework" md:"Framework"`
	TPS         int64  `json:"TPS" md:"TPS"`
	Min         int64  `json:"Min" md:"Min" fmt:"duration" tpn:"opt"`
	Avg         int64  `json:"Avg" md:"Avg" fmt:"duration" tpn:"opt"`
	Max         int64  `json:"Max" md:"Max" fmt:"duration" tpn:"opt"`
	TP50        int64  `json:"TP50" md:"TP50" fmt:"duration" tpn:"opt"`
	TP75        int64  `json:"TP75" md:"TP75" fmt:"duration" tpn:"opt"`
	TP90        int64  `json:"TP90" md:"TP90" fmt:"duration" tpn:"opt"`
	TP95        int64  `json:"TP95" md:"TP95" fmt:"duration" tpn:"opt"`
	TP99        int64  `json:"TP99" md:"TP99" fmt:"duration" tpn:"opt"`
	Used        int64  `json:"Used" md:"Used" fmt:"duration"`
	Total       int    `json:"Total" md:"Total"`
	Success     uint32 `json:"Success" md:"Success"`
	Failed      uint32 `json:"Failed" md:"Failed"`
	Concurrency int    `json:"Concurrency" md:"Concurrency"`
}

func (r *ConnectionsReport) Type() string {
	return "Connections"
}

func (r *ConnectionsReport) Name() string {
	return fmt.Sprintf("%s-Connections", r.Framework)
}

func (r *ConnectionsReport) Headers() []string {
	return ConnectionsReportMarkdownHeaders
}

func (r *ConnectionsReport) Fields(enableTPN bool) []string {
	return ObjFieldValues(r, enableTPN)
}

func (r *ConnectionsReport) SetPprofData(data []byte) {
}

func (r *ConnectionsReport) PprofCPU() []byte {
	return nil
}

func (r *ConnectionsReport) PprofMEM() []byte {
	return nil
}

func (r *ConnectionsReport) String(enableTPN bool) string {
	return ObjString(r, enableTPN)
}
