package report

import (
	"encoding/json"
	"fmt"
	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"
	"math"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type Report interface {
	Type() string
	Name() string
	Headers() []string
	Fields(bool) []string
	String(bool) string
	PprofCPU() []byte
	PprofMEM() []byte
}

// The orders a report table can be written in, as -sort takes them.
const (
	// SortResult puts the best result first: TPS for Connections, and TPS
	// then EER for BenchEcho and BenchRate, whose TPS is the packets the
	// clients read back off the server per second. The fields tagged rank:"1", rank:"2" and so on are
	// what it compares, in that order. Rows that tie on all of them keep the
	// framework order between them, so a run is reproducible rather than
	// merely sorted.
	SortResult = "result"

	// SortFramework is the order config.FrameworkList lists the frameworks
	// in, which is what every report was written in before this existed.
	// It puts the same framework on the same row across every table and
	// across runs, whatever it scored. It is not the order the scripts
	// build and run them in: script/config.sh has a frameworks list of its
	// own carrying the same names in a different order, and only this one
	// reaches a report.
	SortFramework = "framework"
)

// DefaultSort is the order a caller that names none gets. A report is read to
// compare frameworks, so it is ranked by default.
const DefaultSort = SortResult

// SortOrders lists the orders, for a flag's usage text and its validation.
func SortOrders() []string { return []string{SortResult, SortFramework} }

// ValidateSort reports whether order names an order, so that a caller can
// refuse a misspelled flag rather than quietly writing the report in the
// other order.
func ValidateSort(order string) error {
	for _, v := range SortOrders() {
		if order == v {
			return nil
		}
	}
	return fmt.Errorf("report: unknown sort order %q, want one of %v", order, SortOrders())
}

// rankField is one field a report is ranked by, and the column it is shown in.
type rankField struct {
	rank   int
	index  int
	header string
}

// rankFields lists the fields of r tagged rank:"N", in rank order.
func rankFields(r Report) []rankField {
	typ := reflect.Indirect(reflect.ValueOf(r)).Type()
	var fields []rankField
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if rank, err := strconv.Atoi(field.Tag.Get("rank")); err == nil {
			fields = append(fields, rankField{rank, i, field.Tag.Get("md")})
		}
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].rank < fields[j].rank })
	return fields
}

// RankKeys is what SortResult ranks r by, most significant first: the values
// of its rank-tagged fields.
func RankKeys(r Report) []float64 {
	value := reflect.Indirect(reflect.ValueOf(r))
	var keys []float64
	for _, f := range rankFields(r) {
		v := value.Field(f.index)
		switch {
		case v.CanInt():
			keys = append(keys, float64(v.Int()))
		case v.CanUint():
			keys = append(keys, float64(v.Uint()))
		case v.CanFloat():
			keys = append(keys, v.Float())
		}
	}
	return keys
}

// rankedBefore reports whether a ranks above b: bigger on the first key they
// differ on.
func rankedBefore(a, b []float64) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// Percent is how a row's result reads against the best in its table: floored,
// so that only the best shows 100%, and 0% for a table where nothing scored.
//
// The best is 100% by comparison rather than by division: for one float in
// twenty or so, EER among them, value*100/value is 99.99999999999999, which
// floored left a table with no row at 100%. The same rounding put an exact
// fraction a hair under its percent - half the best read 49% - so the rest
// are nudged up by far less than any two results could be told apart by, and
// held under 100%, which stays the best's.
func Percent(value, best float64) string {
	if best <= 0 || value <= 0 {
		return "0%"
	}
	if value >= best {
		return "100%"
	}
	percent := int64(math.Floor(value*100/best + percentSlack))
	return fmt.Sprintf("%d%%", min(percent, 99))
}

// percentSlack is the nudge Percent gives a quotient before flooring it: many
// times the error a division leaves, and nothing next to a percent.
const percentSlack = 1e-9

// withPercent appends each row's percentage of the best to its cell in column
// col, whatever order the rows are in. The value and the percentage are each
// right-aligned, so every cell has one width, which the table's centring then
// leaves alone:
//
//	1000000 100%
//	 123456  12%
func withPercent(rows [][]string, col int, values []float64) {
	best := 0.0
	for _, v := range values {
		best = math.Max(best, v)
	}
	percents := make([]string, len(rows))
	valueLen, percentLen := 0, 0
	for i, row := range rows {
		percents[i] = Percent(values[i], best)
		valueLen = max(valueLen, len(row[col]))
		percentLen = max(percentLen, len(percents[i]))
	}
	for i, row := range rows {
		row[col] = strings.Repeat(" ", valueLen-len(row[col])) + row[col] + " " +
			strings.Repeat(" ", percentLen-len(percents[i])) + percents[i]
	}
}

// SortReports orders reports in place and returns them.
//
// ReadReports builds the slice in config.FrameworkList order, so SortFramework
// is already what it holds and only SortResult has anything to do. The sort is
// stable, which is what leaves a tie - two frameworks that scored the same, or
// a benchmark that did not run and left both at zero - in framework order
// instead of in whatever order the sort happened to land on. An unknown order
// is left alone; ValidateSort is where a caller catches that.
func SortReports(reports []Report, order string) []Report {
	if order == SortResult {
		sort.SliceStable(reports, func(i, j int) bool {
			return rankedBefore(RankKeys(reports[i]), RankKeys(reports[j]))
		})
	}
	return reports
}

// EER is the throughput a server got for each percent of a CPU core it spent,
// or 0 when there is nothing to divide by. Both callers go through this rather
// than dividing for themselves: a server whose CPU samples did not arrive
// leaves CPUAvg at 0, and the +Inf that came out of that division took the
// whole row out of the report file with it, since encoding/json refuses to
// marshal Inf and NaN and ToFile's error was dropped.
func EER(throughput, cpuAvg float64) float64 {
	if cpuAvg <= 0 || math.IsNaN(cpuAvg) || math.IsInf(throughput, 0) || math.IsNaN(throughput) {
		return 0
	}
	eer := throughput / cpuAvg
	if math.IsInf(eer, 0) || math.IsNaN(eer) {
		return 0
	}
	return eer
}

func JSON(report Report) string {
	b, _ := json.Marshal(report)
	return string(b)
}

func Markdown(reports []Report, enableTPN bool, order string, filter func(string) bool) string {
	if len(reports) == 0 {
		return ""
	}
	if filter == nil {
		filter = func(string) bool { return true }
	}
	reports = SortReports(reports, order)

	// Every rank column carries each row's share of the best in it, in
	// either order.
	rows := make([][]string, len(reports))
	keys := make([][]float64, len(reports))
	for i, v := range reports {
		rows[i] = v.Fields(enableTPN)
		keys[i] = RankKeys(v)
	}
	headers := reports[0].Headers()
	ranks := rankFields(reports[0])
	for k, rank := range ranks {
		for col, header := range headers {
			if header == rank.header {
				values := make([]float64, len(reports))
				for i := range reports {
					values[i] = keys[i][k]
				}
				withPercent(rows, col, values)
				break
			}
		}
	}

	// A copy, since the headers are shared by every table of this type.
	title := append([]string(nil), Headers(reports[0], filter)...)
	for _, rank := range ranks {
		for col := range title {
			if title[col] == rank.header {
				title[col] += RankMarker(rank.rank)
				break
			}
		}
	}
	for i, row := range rows {
		rows[i] = filtFieldsByHeaders(row, filter)
	}
	return markdownTable(title, rows)
}

// RankMarker is what a rank column's title carries after its name, in either
// order, after a space: "[↓1]" on the key the rows are ranked by, highest
// first, "[↓2]" on the
// one that breaks a tie on it, and so on.
func RankMarker(rank int) string {
	return " [↓" + strconv.Itoa(rank) + "]"
}

// ConsoleSection is how a report table reads in the console: a rule, its
// name, and the table between blank lines, or a note that there is none.
func ConsoleSection(name, table string) string {
	if table == "" {
		table = "(no results)\n"
	}
	return logging.LongLine + "[" + name + "]\n\n" + table + "\n"
}

func Filename(base, preffix, suffix string) string {
	return "./output/report/" + preffix + base + suffix
}

func WriteFile(filename, data string) error {
	return os.WriteFile(filename, []byte(data), 0666)
}

func ToFile(r Report, preffix, suffix string) error {
	if r.PprofCPU() != nil {
		cpuPprofFilename := Filename(r.Name(), preffix, suffix+".pprof.cpu")
		err := os.WriteFile(cpuPprofFilename, r.PprofCPU(), 0666)
		if err != nil {
			return err
		}
	}

	if r.PprofMEM() != nil {
		memPprofFilename := Filename(r.Name(), preffix, suffix+".pprof.mem")
		err := os.WriteFile(memPprofFilename, r.PprofMEM(), 0666)
		if err != nil {
			return err
		}
	}

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

func Fields(r Report, enableTPN bool, filter func(string) bool) []string {
	return filtFieldsByHeaders(r.Fields(enableTPN), filter)
}

func GenerateConnectionsReports(preffix, suffix string, enableTPN bool, order string, filter func(string) bool) string {
	create := func(framework string) Report {
		return &ConnectionsReport{Framework: framework, Lang: config.FrameworkLang(framework)}
	}
	return GenerateReports(preffix, suffix, enableTPN, order, create, filter)
}

func GenerateBenchEchoReports(preffix, suffix string, enableTPN bool, order string, filter func(string) bool) string {
	create := func(framework string) Report {
		return &BenchEchoReport{Framework: framework, Lang: config.FrameworkLang(framework)}
	}
	return GenerateReports(preffix, suffix, enableTPN, order, create, filter)
}

func GenerateBenchRateReports(preffix, suffix string, enableTPN bool, order string, filter func(string) bool) string {
	return Markdown(ReadBenchRateReports(preffix, suffix), enableTPN, order, filter)
}

func ReadConnectionsReports(preffix, suffix string) []Report {
	create := func(framework string) Report {
		return &ConnectionsReport{Framework: framework, Lang: config.FrameworkLang(framework)}
	}
	return ReadReports(preffix, suffix, create)
}

func ReadBenchEchoReports(preffix, suffix string) []Report {
	create := func(framework string) Report {
		return &BenchEchoReport{Framework: framework, Lang: config.FrameworkLang(framework)}
	}
	return ReadReports(preffix, suffix, create)
}

func ReadBenchRateReports(preffix, suffix string) []Report {
	create := func(framework string) Report {
		return &BenchRateReport{Framework: framework, Lang: config.FrameworkLang(framework)}
	}
	reports := ReadReports(preffix, suffix, create)
	for _, r := range reports {
		r.(*BenchRateReport).fillTPS()
	}
	return reports
}

// GenerateSummary is the Summary table of the run the three reports' files
// are from.
func GenerateSummary(preffix, suffix string) string {
	return Summary(ReadConnectionsReports(preffix, suffix), ReadBenchEchoReports(preffix, suffix),
		ReadBenchRateReports(preffix, suffix))
}

// ReadReports reads the report every framework has in files, in
// config.FrameworkList order. create fills in the Lang column from the config,
// which a file written before the column existed does not carry.
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

func GenerateReports(preffix, suffix string, enableTPN bool, order string, create func(framework string) Report, filter func(string) bool) string {
	reports := ReadReports(preffix, suffix, create)
	return Markdown(reports, enableTPN, order, filter)
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

func filtFieldsByHeaders(values []string, filter func(string) bool) []string {
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
