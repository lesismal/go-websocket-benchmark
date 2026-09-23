package report

import (
	"reflect"
	"strings"
)

// SummaryParameters is the order the Summary table lists the run's parameters
// in: the names the report fields are tagged summary:"<name>" with. A tagged
// name missing from here still gets a row, after these; benchcli-uwscpp reads
// this list for the same order.
var SummaryParameters = []string{
	"Client",
	"Pool",
	"Conns",
	"Payload",
	"Dial Concurrency",
	"Echo Concurrency",
	"Echo Total",
	"Rate Concurrency",
	"Rate Duration",
	"Rate SendRate",
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
// the rows disagree on, which the pool does for a server that installs none,
// lists each value with the frameworks that had it:
//
//	fib_adaptive (fib, fnet); - (fasthttp)
func Summary(tables ...[]Report) string {
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
	for _, name := range summaryOrder(names) {
		rows = append(rows, []string{name, summaryString(values[name])})
	}
	return markdownTable([]string{"Parameter", "Value"}, rows)
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

// summaryOrder puts names in SummaryParameters order, and any it does not
// list after them in the order they were found.
func summaryOrder(names []string) []string {
	found := map[string]bool{}
	for _, name := range names {
		found[name] = true
	}
	ordered := make([]string, 0, len(names))
	for _, name := range SummaryParameters {
		if found[name] {
			ordered = append(ordered, name)
			delete(found, name)
		}
	}
	for _, name := range names {
		if found[name] {
			ordered = append(ordered, name)
		}
	}
	return ordered
}
