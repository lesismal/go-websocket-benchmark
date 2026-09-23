package report

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/lesismal/perf"
)

// Init builds the markdown headers of the three report tables.
//
// A report's columns are its struct's fields, in the order they are declared:
// these headers come off them by reflection, ObjFieldValues reads the row the
// same way, and benchcli-uwscpp generates its own schema from the same
// declarations, so moving a field moves that column in every table both
// clients write. Framework comes first, since it is what a row is about, then
// Lang, the language its server is written in (config.FrameworkLang), then the
// client that measured it and the pool its server installed.
//
// A field tagged md:"-" is left out of the tables and the console, and kept in
// the JSON: that is how TP50, TP75, TP90, CPU Min and MEM Min are still
// recorded without widening every table printed.
//
// A field tagged summary:"<name>" is one of the run's parameters - the client,
// the pool, the connections, the payload, each benchmark's concurrency and so
// on - and is shown once, under that name, in the Summary table in front of
// the three rather than as a column of every row; see Summary. The console
// block each benchmark prints as it finishes still carries it.
func Init(enableTPN bool) {
	headers := func(typ reflect.Type) []string {
		var headers []string
		for i := 0; i < typ.NumField(); i++ {
			if field := typ.Field(i); tableColumn(field, enableTPN) {
				headers = append(headers, field.Tag.Get("md"))
			}
		}
		return headers
	}
	ConnectionsReportMarkdownHeaders = headers(reflect.TypeOf(ConnectionsReport{}))
	BenchEchoReportMarkdownHeaders = headers(reflect.TypeOf(BenchEchoReport{}))
	BenchRateReportMarkdownHeaders = headers(reflect.TypeOf(BenchRateReport{}))
}

// tableColumn is whether field is a column of its report's table.
func tableColumn(field reflect.StructField, enableTPN bool) bool {
	if field.Tag.Get("md") == "-" || field.Tag.Get("summary") != "" {
		return false
	}
	return enableTPN || field.Tag.Get("tpn") == ""
}

// clientName is how the Client column shows the client that measured a row:
// "go", "uwscpp" or "rust". The JSON keeps the full name, "benchcli-go",
// "benchcli-uwscpp" or "benchcli-rust", which is the directory it was built
// from.
func clientName(name string) string {
	return strings.TrimPrefix(name, "benchcli-")
}

// cellString is how a table shows the value of field.
func cellString(field reflect.StructField, fieldValue reflect.Value) string {
	switch field.Tag.Get("fmt") {
	case "client":
		return clientName(fieldValue.String())
	case "mem":
		if fieldValue.CanInt() {
			return perf.I2MemString(uint64(fieldValue.Int()))
		} else if fieldValue.CanUint() {
			return perf.I2MemString(uint64(fieldValue.Uint()))
		}
		return ""
	case "duration":
		return perf.I2TimeString(fieldValue.Int())
	}
	switch field.Type.Name() {
	case "string":
		return fieldValue.String()
	case "float32", "float64":
		return fmt.Sprintf("%.2f", fieldValue.Float())
	default:
		return fmt.Sprintf("%v", fieldValue)
	}
}

// ObjFieldValues is the row obj adds to its table, one cell per header.
func ObjFieldValues(obj interface{}, enableTPN bool) []string {
	values := []string{}
	value := reflect.Indirect(reflect.ValueOf(obj))
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		if field := typ.Field(i); tableColumn(field, enableTPN) {
			values = append(values, cellString(field, value.Field(i)))
		}
	}
	return values
}

func ObjString(obj Report, enableTPN bool) string {
	ret := ""
	headers := []string{}
	values := []string{}
	typ := reflect.TypeOf(obj)
	value := reflect.ValueOf(obj)
	if typ.Kind() == reflect.Ptr && typ.Elem().Kind() == reflect.Struct {
		typ = typ.Elem()
		value = value.Elem()
	}
	typHeader := "BenchType"
	maxHeaderLen := len(typHeader)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		header := field.Tag.Get("md")
		if header == "-" {
			continue
		}
		if !enableTPN {
			isTPN := field.Tag.Get("tpn") != ""
			if isTPN {
				continue
			}
		}
		headers = append(headers, header)
		if len(header) > maxHeaderLen {
			maxHeaderLen = len(header)
		}

		fieldValue := value.FieldByName(field.Name)
		switch field.Tag.Get("fmt") {
		case "client":
			values = append(values, clientName(fieldValue.String()))
		case "cpu":
			values = append(values, fmt.Sprintf("%.2f%%", fieldValue.Float()))
		case "mem":
			if fieldValue.CanInt() {
				values = append(values, perf.I2MemString(uint64(fieldValue.Int())))
			} else if fieldValue.CanUint() {
				values = append(values, perf.I2MemString(uint64(fieldValue.Uint())))
			} else {
				values = append(values, "")
			}
		case "duration":
			values = append(values, perf.I2TimeString(fieldValue.Int()))
		case "-":
		default:
			typName := field.Type.Name()
			switch typName {
			case "string":
				values = append(values, fieldValue.String())
			case "float32", "float64":
				values = append(values, fmt.Sprintf("%.2f", fieldValue.Float()))
			default:
				values = append(values, fmt.Sprintf("%v", fieldValue))
			}
		}
	}
	frameworkHeader := ""
	for i, v := range headers {
		isFramework := v == "Framework"
		for len(v) < maxHeaderLen {
			v += " "
		}
		if isFramework {
			frameworkHeader = v
		}
		headers[i] = v
	}
	for len(typHeader) < maxHeaderLen {
		typHeader += " "
	}
	for i, v := range headers {
		if v == frameworkHeader {
			ret += typHeader + ": " + obj.Type() + "\n"
		}
		ret += v + ": " + values[i]
		if i != len(headers)-1 {
			ret += "\n"
		}
	}
	return ret
}
