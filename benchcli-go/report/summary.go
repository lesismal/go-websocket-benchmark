package report

import (
	"reflect"
	"slices"
	"strings"

	"go-websocket-benchmark/config"
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
// read this list for the same order and the same words.
var SummaryParameters = []SummaryParameter{
	{"Project", "what this run benchmarks (-project)"},
	{"Client", "benchmark client, as language-framework: cpp-uwebsockets, rust-tokio_tungstenite or go-nbio"},
	{"Pool", "task pool, used by Go event-loop frameworks only"},
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
func Summary(project string, tables ...[]Report) string {
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

	var rows [][]string
	if project != "" {
		rows = append(rows, []string{"Project", project, summaryDescription("Project")})
	}
	for _, name := range summaryOrder(names) {
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
// which the row's description says - plus uwebsockets' "logicpool", its own
// logic thread pool, and "inline" for uwebsockets-inline, as for the Go -inline
// entries. A "(...)" suffix on a value is dropped:
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
