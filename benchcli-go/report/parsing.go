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
// the client that measured it and the pool its server installed.
//
// A field tagged md:"-" is left out of the tables and the console, and kept in
// the JSON: that is how TP50, TP75, TP90, CPU Min and MEM Min are still
// recorded without widening every table printed.
func Init(enableTPN bool) {
	appendTPNHeadder := func(headers []string, field reflect.StructField) []string {
		header := field.Tag.Get("md")
		if header != "-" {
			if enableTPN {
				headers = append(headers, header)
				return headers
			}
			isTPN := field.Tag.Get("tpn") != ""
			if !isTPN {
				headers = append(headers, header)
			}
		}
		return headers
	}
	typ := reflect.TypeOf(ConnectionsReport{})
	for i := 0; i < typ.NumField(); i++ {
		ConnectionsReportMarkdownHeaders = appendTPNHeadder(ConnectionsReportMarkdownHeaders, typ.Field(i))
	}

	typ = reflect.TypeOf(BenchEchoReport{})
	for i := 0; i < typ.NumField(); i++ {
		BenchEchoReportMarkdownHeaders = appendTPNHeadder(BenchEchoReportMarkdownHeaders, typ.Field(i))
	}

	typ = reflect.TypeOf(BenchRateReport{})
	for i := 0; i < typ.NumField(); i++ {
		header := typ.Field(i).Tag.Get("md")
		if header != "-" {
			BenchRateReportMarkdownHeaders = append(BenchRateReportMarkdownHeaders, header)
		}
	}
}

// clientName is how the Client column shows the client that measured a row:
// "go" or "uwscpp". The JSON keeps the full name, "benchcli-go" or
// "benchcli-uwscpp", which is the directory it was built from.
func clientName(name string) string {
	return strings.TrimPrefix(name, "benchcli-")
}

func ObjFieldValues(obj interface{}, enableTPN bool) []string {
	values := []string{}
	typ := reflect.TypeOf(obj)
	value := reflect.ValueOf(obj)
	if typ.Kind() == reflect.Ptr && typ.Elem().Kind() == reflect.Struct {
		typ = typ.Elem()
		value = value.Elem()
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Tag.Get("md") == "-" {
			continue
		}
		fieldValue := value.FieldByName(field.Name)

		isTPN := field.Tag.Get("tpn") != ""
		if !enableTPN && isTPN {
			continue
		}

		switch field.Tag.Get("fmt") {
		case "client":
			values = append(values, clientName(fieldValue.String()))
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
