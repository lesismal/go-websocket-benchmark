package report

import (
	"encoding/json"
	"os"

	"github.com/lesismal/perf"
)

type Report interface {
	Tag() string
	Headers() []string
	Fields() []string
	String() string
}

func Filename(name, preffix, suffix string) string {
	return "./output/report/" + preffix + name + suffix + ".json"
}

func ToFile(report Report, preffix, suffix string) error {
	b, err := json.Marshal(report)
	if err != nil {
		return err
	}
	filename := Filename(report.Tag(), preffix, suffix)
	return os.WriteFile(filename, b, 0666)
}

func JSON(report Report) string {
	b, _ := json.Marshal(report)
	return string(b)
}

func Markdown(reports []Report, filter func(string) bool) string {
	if len(reports) == 0 {
		return ""
	}

	table := perf.NewTable()
	table.SetTitle(Headers(reports[0], filter))
	for _, v := range reports {
		table.AddRow(Fields(v, filter))
	}

	return table.Markdown()
}

func Headers(report Report, filter func(string) bool) []string {
	return filtHeaders(report.Headers(), filter)
}

func Fields(report Report, filter func(string) bool) []string {
	return filtFieldsByHeaders(report.Headers(), report.Fields(), filter)
}

// func Join(reports []Report) Report {
// 	return nil
// }

func filtHeaders(headers []string, filter func(string) bool) []string {
	retValues := headers[0:0]
	if filter != nil {
		for _, v := range headers {
			if filter(v) {
				retValues = append(retValues, v)
			}
		}
	}
	return retValues
}

func filtFieldsByHeaders(headers []string, values []string, filter func(string) bool) []string {
	retValues := values[0:0]
	if filter != nil {
		for _, v := range values {
			if filter(v) {
				retValues = append(retValues, v)
			}
		}
	}
	return retValues
}
