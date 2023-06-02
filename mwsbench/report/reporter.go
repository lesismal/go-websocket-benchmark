package report

import "github.com/lesismal/perf"

type Report interface {
	Headers() []string
	Fields() []string
	String() string
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

func Join(reports []Report) Report {
	return nil
}

func Headers(report Report, filter func(string) bool) []string {
	return filt(report.Headers(), filter)
}

func Fields(report Report, filter func(string) bool) []string {
	return filt(report.Fields(), filter)
}

func filt(values []string, filter func(string) bool) []string {
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
