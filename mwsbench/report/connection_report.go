package report

var (
	connectionReportMarkdownHeaders = []string{}
)

type ConnectionReport struct {
}

func (r *ConnectionReport) Headers() []string {
	return nil
}

func (r *ConnectionReport) Fields() []string {
	return nil
}

func (r *ConnectionReport) String() string {
	return ""
}
