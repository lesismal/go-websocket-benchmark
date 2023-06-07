package report

import (
	"encoding/json"
	"go-websocket-benchmark/config"
	"os"

	"github.com/lesismal/perf"
)

type Report interface {
	Type() string
	Name() string
	Headers() []string
	Fields() []string
	String() string
}

func JSON(report Report) string {
	b, _ := json.Marshal(report)
	return string(b)
}

func Markdown(reports []Report, filter func(string) bool) string {
	if len(reports) == 0 {
		return ""
	}
	if filter == nil {
		filter = func(string) bool { return true }
	}

	table := perf.NewTable()
	table.SetTitle(Headers(reports[0], filter))
	for _, v := range reports {
		table.AddRow(Fields(v, filter))
	}

	return table.Markdown()
}

func Filename(base, preffix, suffix string) string {
	return "./output/report/" + preffix + base + suffix
}

func WriteFile(filename, data string) error {
	return os.WriteFile(filename, []byte(data), 0666)
}

func ToFile(r Report, preffix, suffix string) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	filename := Filename(r.Name(), preffix, suffix+".json")
	return os.WriteFile(filename, b, 0666)
}

func Headers(r Report, filter func(string) bool) []string {
	return filtHeaders(r.Headers(), filter)
}

func Fields(r Report, filter func(string) bool) []string {
	return filtFieldsByHeaders(r.Headers(), r.Fields(), filter)
}

func GenerateConnectionsReports(preffix, suffix string, filter func(string) bool) string {
	create := func(framework string) Report {
		return &ConnectionsReport{Framework: framework}
	}
	return GenerateReports(preffix, suffix, create, filter)
}

func GenerateBenchEchoReports(preffix, suffix string, filter func(string) bool) string {
	create := func(framework string) Report {
		return &BenchEchoReport{Framework: framework}
	}
	return GenerateReports(preffix, suffix, create, filter)
}

func GenerateBenchRateReports(preffix, suffix string, filter func(string) bool) string {
	create := func(framework string) Report {
		return &BenchRateReport{Framework: framework}
	}
	return GenerateReports(preffix, suffix, create, filter)
}

func ReadConnectionsReports(preffix, suffix string) []Report {
	create := func(framework string) Report {
		return &ConnectionsReport{Framework: framework}
	}
	return ReadReports(preffix, suffix, create)
}

func ReadBenchEchoReports(preffix, suffix string) []Report {
	create := func(framework string) Report {
		return &BenchEchoReport{Framework: framework}
	}
	return ReadReports(preffix, suffix, create)
}

func ReadReports(preffix, suffix string, create func(framework string) Report) []Report {
	reports := make([]Report, 0, len(config.FrameworkList))
	var reportItem Report
	for _, v := range config.FrameworkList {
		reportItem = create(v)
		filename := Filename(reportItem.Name(), preffix, suffix+".json")
		b, err := os.ReadFile(filename)
		if err != nil {
			// logging.Printf("ReadFile %v failed: %v", v, err)
			continue
		}

		err = json.Unmarshal(b, reportItem)
		if err != nil {
			// logging.Printf("Unmarshal Report %v failed: %v", v, err)
			continue
		}
		reports = append(reports, reportItem)
	}

	return reports
}

func GenerateReports(preffix, suffix string, create func(framework string) Report, filter func(string) bool) string {
	reports := ReadReports(preffix, suffix, create)
	return Markdown(reports, filter)
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
