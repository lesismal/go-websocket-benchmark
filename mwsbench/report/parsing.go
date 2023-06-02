package report

import (
	"fmt"
	"reflect"

	"github.com/lesismal/perf"
)

func init() {
	typ := reflect.TypeOf(ConnectionsReport{})
	for i := 0; i < typ.NumField(); i++ {
		header := typ.Field(i).Tag.Get("md")
		ConnectionsReportMarkdownHeaders = append(ConnectionsReportMarkdownHeaders, header)
	}

	typ = reflect.TypeOf(BenchEchoReport{})
	for i := 0; i < typ.NumField(); i++ {
		header := typ.Field(i).Tag.Get("md")
		benchEchoReportMarkdownHeaders = append(benchEchoReportMarkdownHeaders, header)
	}
}

func ObjFieldValues(obj interface{}) []string {
	values := []string{}
	typ := reflect.TypeOf(obj)
	value := reflect.ValueOf(obj)
	if typ.Kind() == reflect.Ptr && typ.Elem().Kind() == reflect.Struct {
		typ = typ.Elem()
		value = value.Elem()
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fieldValue := value.FieldByName(field.Name)
		switch field.Tag.Get("fmt") {
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

func ObjString(obj interface{}) string {
	ret := ""
	headers := []string{}
	values := []string{}
	typ := reflect.TypeOf(obj)
	value := reflect.ValueOf(obj)
	if typ.Kind() == reflect.Ptr && typ.Elem().Kind() == reflect.Struct {
		typ = typ.Elem()
		value = value.Elem()
	}
	maxHeaderLen := 0
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		header := field.Tag.Get("md")
		headers = append(headers, header)
		if len(header) > maxHeaderLen {
			maxHeaderLen = len(header)
		}

		fieldValue := value.FieldByName(field.Name)
		switch field.Tag.Get("fmt") {
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
	for i, v := range headers {
		for len(v) < maxHeaderLen {
			v += " "
		}
		headers[i] = v
	}
	for i, v := range headers {
		ret += v + ": " + values[i]
		if i != len(headers)-1 {
			ret += "\n"
		}
	}
	return ret
}

// func (r *BenchEchoReport) MarkdownHeaderFilter(v string) bool {
// 	return benchEchoReportMarkdownHeaderFilterMap[v]
// }

// func (r *BenchEchoReport) MarkdownFieldFilter(idx int) bool {
// 	return benchEchoReportMarkdownFieldFilterMap[idx]
// }

// var(

// benchEchoReportMarkdownHeaderFilterMap = map[string]bool{}
// benchEchoReportMarkdownFieldFilterMap  = map[int]bool{}
//
//	benchEchoReportHeaders = []string{
//		"Framework",
//		"Conns",
//		"Concurrency",
//		"Payload",
//		"Total",
//		"Success",
//		"Failed",
//		"Used",
//		"CPU Min",
//		"CPU Avg",
//		"CPU Max",
//		"MEM Min",
//		"MEM Avg",
//		"MEM Max",
//		"Min",
//		"Avg",
//		"Max",
//		"TPS",
//		"TP50",
//		"TP75",
//		"TP90",
//		"TP95",
//		"TP99",
//	}
// )

// func init() {
// 	for i, v := range benchEchoReportHeaders {
// 		switch v {
// 		case "Framework", "Conns", "Payload", "Total", "Success", "Failed", "Used", "CPU Avg", "MEM Avg", "Avg", "TPS", "TP50", "TP90", "TP99":
// 			benchEchoReportMarkdownHeaderFilterMap[v] = true
// 			benchEchoReportMarkdownFieldFilterMap[i] = true
// 		default:
// 		}
// 	}
// }
