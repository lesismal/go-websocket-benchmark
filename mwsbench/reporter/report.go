package reporter

type Report interface {
	Headers() []string
	Fields() []string
}

func Markdown(report Report) string {
	return ""
}

func Join(reports []Report) Report {
	return nil
}
