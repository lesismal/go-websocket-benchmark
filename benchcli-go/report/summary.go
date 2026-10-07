package report

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"reflect"
	"slices"
	"strings"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"
)

// SummaryParameter is one row of the Summary table: a name report fields are
// tagged summary:"<name>" with, and what its Description column says.
type SummaryParameter struct {
	Name        string
	Description string
}

// SummaryParameters is the order the Summary table lists the run's parameters
// in, and their descriptions. A tagged name missing from here still gets a
// row, after these, with no description; benchcli-uwscpp and benchcli-rust
// read this list for the same order and the same words. Pool Size, Event
// Loops, Loops Per CPU and Socket Syscalls are server settings rather than
// report fields; see ServerParameter.
var SummaryParameters = []SummaryParameter{
	{"Project", "what this run benchmarks (-project)"},
	{"Client", "benchmark client, as language-framework: cpp-uwebsockets, rust-tokio_tungstenite or go-nbio"},
	{"Pool", "task pool, used by Go event-loop frameworks only"},
	{"Pool Size", "task pool sizing, 0 for the pool's own default (BENCH_TASKPOOL_MIN/_MAX/_QUEUE)"},
	{"Event Loops", "event loops per server, threads for Rust and C++, 0 for each framework's own default (-eventloops)"},
	{"Loops Per CPU", "uwebsockets event loops per CPU, 0 for one per CPU; -eventloops overrides it (BENCH_UWS_LOOPS_PER_CPU)"},
	{"Socket Syscalls", "fib socket calls: true recvfrom/sendto/sendmsg, false read/write/writev; Linux only (-socketsyscalls)"},
	{"Conns", "connections each benchmark runs over"},
	{"Payload", "message size in bytes"},
	{"Dial Concurrency", "connections dialed at once (-dc)"},
	{"Echo Concurrency", "echo requests in flight at once (-ec)"},
	{"Echo Total", "echo round trips per framework (-en)"},
	{"Rate Concurrency", "connections sending at once in BenchPipeline (-rc)"},
	{"Rate Duration", "how long BenchPipeline sends for (-rd)"},
	{"Rate SendRate", "messages sent to each connection per second (-rr)"},
	{"Rate Pipeline", "messages merged into one write in BenchPipeline (-rpl)"},
	{"Echo Pprof", "client sampled Go servers' pprof in BenchEcho (-ep); others never are"},
	{"Rate Pprof", "client sampled Go servers' pprof in BenchPipeline (-rp); others never are"},
}

// PprofSetting is a report's EchoPprof or RatePprof: whether -ep or -rp was on.
func PprofSetting(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

// DefaultProject is the Summary's Project row when the report step is given
// no -project. benchcli-uwscpp and benchcli-rust default to the same name.
const DefaultProject = "GO-WEBSOCKET-BENCHMARK"

// ServerParameter is one of the server settings script/config.sh gave a run -
// the event loops, the pool's sizing and the like - and the frameworks of the
// run that take it. The clients never see these, so no report carries them:
// the driver that starts the run writes them to ServerParametersFile, beside
// the reports, and the Summary gives each a row of its own, its Description
// naming those frameworks. See bench_write_server_parameters in script/env.sh.
type ServerParameter struct {
	Name       string   `json:"Name"`
	Value      string   `json:"Value"`
	Frameworks []string `json:"Frameworks"`
}

// ServerParametersFile is where a run's ServerParameters are: one file for the
// run, whatever -preffix and -suffix its reports were written with, since
// every one of them was measured against the same servers.
// benchcli-uwscpp and benchcli-rust read the same file.
const ServerParametersFile = "./output/report/ServerParameters.json"

// ReadServerParameters reads ServerParametersFile. A run started without a
// driver script has none, and a file that cannot be read is logged and taken
// as none: the Summary is then without those rows rather than the report
// without its tables.
func ReadServerParameters() []ServerParameter {
	b, err := os.ReadFile(ServerParametersFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var params []ServerParameter
	if err == nil {
		err = json.Unmarshal(b, &params)
	}
	if err != nil {
		logging.Printf("reading %v failed: %v", ServerParametersFile, err)
		return nil
	}
	return params
}

// serverParameterDescription is the Description of a ServerParameter's row:
// the words SummaryParameters has for it, and the frameworks it reached.
func serverParameterDescription(p ServerParameter) string {
	description := summaryDescription(p.Name)
	if len(p.Frameworks) == 0 {
		return description
	}
	if description != "" {
		description += "; "
	}
	return description + "affects: " + strings.Join(p.Frameworks, ", ")
}

// summaryValue is one value a parameter took, and the frameworks it took it
// for, in the order they were read.
type summaryValue struct {
	value      string
	frameworks []string
}

// Summary is the table of the run's parameters, taken off the summary-tagged
// fields of every row of every report. A parameter every row agrees on - the
// client, the payload, the concurrency a flag set - reads as that value. One
// the rows disagree on lists each value with the frameworks that had it:
//
//	20000 (fib, fnet); 19998 (fasthttp)
//
// except Pool, which says only which pools ran; see poolSummary. A third
// column describes each parameter. The table is left-aligned, so that a long
// value reads from its start.
//
// project heads the table as its Project row, naming what the run
// benchmarks; it is no field of a report, so it comes from the report step's
// -project rather than from the rows. An empty project leaves the row out, and
// a run with no reports still has no Summary at all.
//
// params are the run's server settings, each a row in SummaryParameters order
// among the others, whose Description ends with the frameworks it affects:
//
//	Event Loops | 4 | event loops per server, ... (-eventloops); affects: fib, fnet
//
// A report field of the same name keeps its own row.
func Summary(project string, params []ServerParameter, tables ...[]Report) string {
	values := map[string][]summaryValue{}
	var names []string
	for _, reports := range tables {
		for _, r := range reports {
			value := reflect.Indirect(reflect.ValueOf(r))
			typ := value.Type()
			framework := value.FieldByName("Framework").String()
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				name := field.Tag.Get("summary")
				if name == "" {
					continue
				}
				if _, seen := values[name]; !seen {
					names = append(names, name)
				}
				values[name] = addSummaryValue(values[name], cellString(field, value.Field(i)), framework)
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	server := map[string]ServerParameter{}
	for _, p := range params {
		if _, seen := values[p.Name]; seen || p.Name == "" {
			continue
		}
		if _, seen := server[p.Name]; !seen {
			names = append(names, p.Name)
		}
		server[p.Name] = p
	}

	var rows [][]string
	if project != "" {
		rows = append(rows, []string{"Project", project, summaryDescription("Project")})
	}
	for _, name := range summaryOrder(names) {
		if p, ok := server[name]; ok {
			rows = append(rows, []string{name, p.Value, serverParameterDescription(p)})
			continue
		}
		// A parameter no report carries - one added after the reports being
		// read were written - has no row rather than an empty one.
		if v := values[name]; len(v) == 1 && v[0].value == "" {
			continue
		}
		text := summaryString(values[name])
		if name == "Pool" {
			text = poolSummary(values[name])
		}
		rows = append(rows, []string{name, text, summaryDescription(name)})
	}
	return markdownTableAligned([]string{"Parameter", "Value", "Description"}, rows, true)
}

func addSummaryValue(values []summaryValue, value, framework string) []summaryValue {
	for i := range values {
		if values[i].value == value {
			for _, f := range values[i].frameworks {
				if f == framework {
					return values
				}
			}
			values[i].frameworks = append(values[i].frameworks, framework)
			return values
		}
	}
	return append(values, summaryValue{value, []string{framework}})
}

func summaryString(values []summaryValue) string {
	if len(values) == 1 {
		return values[0].value
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = v.value + " (" + strings.Join(v.frameworks, ", ") + ")"
	}
	return strings.Join(parts, "; ")
}

// poolSummary is the Pool row: the pools the servers installed, without the
// frameworks, which would make the row as long as the run. Only the Go
// event-loop frameworks install one - the rest report "-" and are left out,
// which the row's description says - plus tokio_tungstenite's "logicpool",
// its own logic thread pool, and "inline" for uwebsockets, as for a Go
// server run -taskpool=inline. A "(...)" suffix on a value is dropped:
//
//	fib_adaptive
func poolSummary(values []summaryValue) string {
	var pools []string
	for _, v := range values {
		pool := v.value
		if i := strings.IndexByte(pool, '('); i > 0 {
			pool = pool[:i]
		}
		if pool == config.TaskPoolNone || pool == "" || slices.Contains(pools, pool) {
			continue
		}
		pools = append(pools, pool)
	}
	if len(pools) == 0 {
		return config.TaskPoolNone
	}
	return strings.Join(pools, ", ")
}

// summaryDescription is what the Description column says about name.
func summaryDescription(name string) string {
	for _, p := range SummaryParameters {
		if p.Name == name {
			return p.Description
		}
	}
	return ""
}

// summaryOrder puts names in SummaryParameters order, and any it does not
// list after them in the order they were found.
func summaryOrder(names []string) []string {
	found := map[string]bool{}
	for _, name := range names {
		found[name] = true
	}
	ordered := make([]string, 0, len(names))
	for _, p := range SummaryParameters {
		if found[p.Name] {
			ordered = append(ordered, p.Name)
			delete(found, p.Name)
		}
	}
	for _, name := range names {
		if found[name] {
			ordered = append(ordered, name)
		}
	}
	return ordered
}
