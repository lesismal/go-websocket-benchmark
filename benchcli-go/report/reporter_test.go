package report

import (
	"math"
	"math/rand"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"go-websocket-benchmark/config"

	"github.com/lesismal/perf"
)

// names reads the frameworks back off a sorted slice, which is the whole of
// what the order has to get right.
func names(reports []Report) []string {
	got := make([]string, 0, len(reports))
	for _, r := range reports {
		switch v := r.(type) {
		case *ConnectionsReport:
			got = append(got, v.Framework)
		case *BenchEchoReport:
			got = append(got, v.Framework)
		case *BenchRateReport:
			got = append(got, v.Framework)
		}
	}
	return got
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSortResultRanksConnectionsAndEchoByTPS(t *testing.T) {
	connections := []Report{
		&ConnectionsReport{Framework: "slow", TPS: 10},
		&ConnectionsReport{Framework: "fast", TPS: 300},
		&ConnectionsReport{Framework: "mid", TPS: 200},
	}
	if got := names(SortReports(connections, SortResult)); !equal(got, []string{"fast", "mid", "slow"}) {
		t.Errorf("Connections ranked %v, want fast, mid, slow", got)
	}

	// EER runs the other way here, so a row ranked by it would come out
	// reversed: TPS is what the order has to read.
	echo := []Report{
		&BenchEchoReport{Framework: "slow", TPS: 10, EER: 900},
		&BenchEchoReport{Framework: "fast", TPS: 300, EER: 1},
		&BenchEchoReport{Framework: "mid", TPS: 200, EER: 50},
	}
	if got := names(SortReports(echo, SortResult)); !equal(got, []string{"fast", "mid", "slow"}) {
		t.Errorf("BenchEcho ranked %v, want fast, mid, slow", got)
	}
}

// TestSortResultRanksRateByTPSThenEER holds the rate benchmark to its TPS, the
// packets the clients read back per second, not to what they sent or to the
// bytes, and breaks a tie on it by EER.
func TestSortResultRanksRateByTPSThenEER(t *testing.T) {
	rate := []Report{
		&BenchRateReport{Framework: "few", SendBytes: 9000, TPS: 10, RecvBytes: 9000, EchoEER: 900},
		&BenchRateReport{Framework: "many-costly", TPS: 90, RecvBytes: 10, EchoEER: 5},
		&BenchRateReport{Framework: "mid", TPS: 50, EchoEER: 1},
		&BenchRateReport{Framework: "many-cheap", TPS: 90, RecvBytes: 10, EchoEER: 50},
	}
	want := []string{"many-cheap", "many-costly", "mid", "few"}
	if got := names(SortReports(rate, SortResult)); !equal(got, want) {
		t.Errorf("BenchRate ranked %v, want %v", got, want)
	}
}

// TestSortFrameworkKeepsTheOrderItWasGiven is the other mode: ReadReports
// builds the slice in config.FrameworkList order and this one leaves it there.
func TestSortFrameworkKeepsTheOrderItWasGiven(t *testing.T) {
	reports := []Report{
		&BenchEchoReport{Framework: "slow", TPS: 10},
		&BenchEchoReport{Framework: "fast", TPS: 300},
		&BenchEchoReport{Framework: "mid", TPS: 200},
	}
	if got := names(SortReports(reports, SortFramework)); !equal(got, []string{"slow", "fast", "mid"}) {
		t.Errorf("SortFramework reordered the rows to %v", got)
	}
}

// TestSortResultIsStableOnTies keeps a run reproducible: rows that scored the
// same, including a benchmark that did not run and left every row at zero,
// come out in the order they were read in rather than in a different one each
// time.
func TestSortResultIsStableOnTies(t *testing.T) {
	reports := []Report{
		&BenchEchoReport{Framework: "a", TPS: 100},
		&BenchEchoReport{Framework: "b", TPS: 100},
		&BenchEchoReport{Framework: "c", TPS: 500},
		&BenchEchoReport{Framework: "d", TPS: 100},
	}
	if got := names(SortReports(reports, SortResult)); !equal(got, []string{"c", "a", "b", "d"}) {
		t.Errorf("ties came out as %v, want c, a, b, d", got)
	}
}

func TestDefaultSortIsResult(t *testing.T) {
	if DefaultSort != SortResult {
		t.Errorf("DefaultSort is %q, want %q", DefaultSort, SortResult)
	}
	if err := ValidateSort(DefaultSort); err != nil {
		t.Errorf("ValidateSort rejected the default: %v", err)
	}
}

func TestValidateSortRejectsAnUnknownOrder(t *testing.T) {
	for _, order := range SortOrders() {
		if err := ValidateSort(order); err != nil {
			t.Errorf("ValidateSort(%q) failed: %v", order, err)
		}
	}
	if err := ValidateSort("tps"); err == nil {
		t.Error("ValidateSort accepted an order no mode is named after")
	}
}

// TestMarkdownWritesTheRowsInOrder checks the table itself rather than the
// slice, since that is what a reader of the report sees.
func TestMarkdownWritesTheRowsInOrder(t *testing.T) {
	Init(false)
	reports := []Report{
		&BenchEchoReport{Framework: "slow", TPS: 10},
		&BenchEchoReport{Framework: "fast", TPS: 300},
		&BenchEchoReport{Framework: "mid", TPS: 200},
	}

	ranked := Markdown(reports, false, SortResult, nil)
	if got := rowOrder(ranked, "fast", "mid", "slow"); !got {
		t.Errorf("SortResult table did not read fast, mid, slow:\n%s", ranked)
	}

	// Markdown sorts in place, so the second call is also a check that the
	// first did not leave the caller's slice in an order it cannot undo.
	byFramework := Markdown([]Report{
		&BenchEchoReport{Framework: "slow", TPS: 10},
		&BenchEchoReport{Framework: "fast", TPS: 300},
		&BenchEchoReport{Framework: "mid", TPS: 200},
	}, false, SortFramework, nil)
	if got := rowOrder(byFramework, "slow", "fast", "mid"); !got {
		t.Errorf("SortFramework table did not read slow, fast, mid:\n%s", byFramework)
	}
}

// rowOrder reports whether want appears in table in that order.
func rowOrder(table string, want ...string) bool {
	at := 0
	for _, v := range want {
		i := strings.Index(table[at:], v)
		if i < 0 {
			return false
		}
		at += i + len(v)
	}
	return true
}

// TestHiddenColumnsStayInTheJSON holds the tables and the console to the
// shorter set of columns, and the JSON to all of them: TP50, TP75, TP90,
// CPU Min and MEM Min are md:"-", the Client column drops the "benchcli-"
// prefix, and BenchRate's EchoEER is headed EER.
func TestHiddenColumnsStayInTheJSON(t *testing.T) {
	Init(true)
	hidden := []string{"TP50", "TP75", "TP90", "CPU Min", "MEM Min", "benchcli-", "EchoEER"}

	echo := &BenchEchoReport{Framework: "gorilla", BenchClient: "benchcli-uwscpp", CPUMin: 1, MEMRSSMin: 1}
	rate := &BenchRateReport{Framework: "gorilla", BenchClient: "benchcli-go", EchoEER: 12.5}
	conns := &ConnectionsReport{Framework: "gorilla", BenchClient: "benchcli-go"}
	for _, r := range []Report{echo, rate, conns} {
		if got, want := len(r.Headers()), len(r.Fields(true)); got != want {
			t.Errorf("%v: %d headers but %d fields", r.Type(), got, want)
		}
		for _, out := range []string{Markdown([]Report{r}, true, SortFramework, nil), r.String(true)} {
			for _, v := range hidden {
				if strings.Contains(out, v) {
					t.Errorf("%v output shows %q:\n%s", r.Type(), v, out)
				}
			}
		}
	}

	table := Markdown([]Report{echo}, true, SortFramework, nil)
	if !strings.Contains(table, "TP95") {
		t.Errorf("BenchEcho table lost a column it should keep:\n%s", table)
	}
	if table := Markdown([]Report{rate}, true, SortFramework, nil); !strings.Contains(table, " EER [↓2] ") ||
		!strings.Contains(table, "12.50") {
		t.Errorf("BenchRate table:\n%s", table)
	}
	if summary := Summary([]Report{echo}, []Report{rate}); !strings.Contains(summary, "uwscpp (gorilla); go (gorilla)") {
		t.Errorf("Summary does not show the clients without their prefix:\n%s", summary)
	}

	for _, v := range []string{`"TP50"`, `"TP75"`, `"TP90"`, `"CPUMin"`, `"MEMMin"`, `"BenchClient":"benchcli-uwscpp"`} {
		if !strings.Contains(JSON(echo), v) {
			t.Errorf("BenchEcho JSON lost %s: %s", v, JSON(echo))
		}
	}
	if !strings.Contains(JSON(rate), `"EchoEER":12.5`) {
		t.Errorf("BenchRate JSON lost EchoEER: %s", JSON(rate))
	}
}

func TestPercent(t *testing.T) {
	for _, c := range []struct {
		value, best float64
		want        string
	}{
		{300, 300, "100%"}, {299, 300, "99%"}, {29, 100, "29%"}, {1, 300, "0%"}, {0, 300, "0%"}, {0, 0, "0%"},
	} {
		if got := Percent(c.value, c.best); got != c.want {
			t.Errorf("Percent(%v, %v) = %q, want %q", c.value, c.best, got, c.want)
		}
	}
}

// TestMarkdownShowsThePercentOfTheBest puts each row's share of the best
// result after it in the ranked column, right-aligned, in either order.
func TestMarkdownShowsThePercentOfTheBest(t *testing.T) {
	Init(false)
	for _, order := range SortOrders() {
		table := Markdown([]Report{
			&BenchEchoReport{Framework: "slow", TPS: 10},
			&BenchEchoReport{Framework: "fast", TPS: 3000},
			&BenchEchoReport{Framework: "mid", TPS: 1500},
		}, false, order, nil)
		for _, cell := range []string{"| 3000 100% |", "| 1500  50% |", "|   10   0% |"} {
			if !strings.Contains(table, cell) {
				t.Errorf("-sort=%v: no %q in:\n%s", order, cell, table)
			}
		}
	}

	table := Markdown([]Report{
		&BenchRateReport{Framework: "a", TPS: 200, EchoEER: 1},
		&BenchRateReport{Framework: "b", TPS: 50, EchoEER: 9},
	}, false, SortResult, nil)
	for _, cell := range []string{"| 200 100% |", "|  50  25% |"} {
		if !strings.Contains(table, cell) {
			t.Errorf("BenchRate: no %q in:\n%s", cell, table)
		}
	}
}

// TestSummaryTakesTheParametersOutOfTheTables moves the run's parameters into
// the Summary table: one value where every row agrees, each value with its
// frameworks where they do not, and none of them left as a column.
func TestSummaryTakesTheParametersOutOfTheTables(t *testing.T) {
	Init(false)
	conns := []Report{
		&ConnectionsReport{Framework: "fib", BenchClient: "benchcli-uwscpp", TaskPool: "fib_adaptive", TPS: 9, Concurrency: 2000},
		&ConnectionsReport{Framework: "fasthttp", BenchClient: "benchcli-uwscpp", TaskPool: "-", TPS: 8, Concurrency: 2000},
		&ConnectionsReport{Framework: "fnet", BenchClient: "benchcli-uwscpp", TaskPool: "fib_adaptive", TPS: 7, Concurrency: 2000},
	}
	echo := []Report{
		&BenchEchoReport{Framework: "fib", BenchClient: "benchcli-uwscpp", TaskPool: "fib_adaptive", Connections: 20000, Concurrency: 10000, Total: 2000000, Payload: 1024},
		&BenchEchoReport{Framework: "fasthttp", BenchClient: "benchcli-uwscpp", TaskPool: "-", Connections: 20000, Concurrency: 10000, Total: 2000000, Payload: 1024},
	}
	rate := []Report{
		&BenchRateReport{Framework: "fib", BenchClient: "benchcli-uwscpp", TaskPool: "fib_adaptive", Duration: 10e9, Connections: 20000, Concurrency: 5000, SendRate: 200, Pipeline: 10, Payload: 1024},
	}
	summary := Summary(conns, echo, rate)
	rows := []string{"Client", "uwscpp", "Pool", "fib_adaptive", "Go event-loop frameworks only", "Conns", "20000",
		"Payload", "1024", "Dial Concurrency", "2000", "Echo Concurrency", "10000", "Echo Total", "2000000",
		"Rate Concurrency", "5000", "Rate Duration", "10.00s", "Rate SendRate", "200", "Rate Pipeline", "10",
		"messages merged into one write in BenchRate (-rpl)"}
	if !rowOrder(summary, rows...) {
		t.Errorf("Summary does not read %v:\n%s", rows, summary)
	}

	for _, table := range []string{Markdown(conns, false, SortResult, nil), Markdown(echo, false, SortResult, nil),
		Markdown(rate, false, SortResult, nil)} {
		for _, column := range []string{"Client", "Pool", "Conns", "Concurrency", "Payload", "Duration", "SendRate", "Pipeline"} {
			if strings.Contains(strings.SplitN(table, "\n", 2)[0], " "+column+" ") {
				t.Errorf("table still has a %v column:\n%s", column, table)
			}
		}
	}
	// Connections keeps its Total, the connections dialed, next to the
	// Success and Failed that count them; BenchEcho's is the -en it was
	// asked for, and moves to the Summary.
	if table := Markdown(echo, false, SortResult, nil); strings.Contains(table, " Total ") {
		t.Errorf("BenchEcho table still has a Total column:\n%s", table)
	}

	// Three columns, left-aligned: every cell a space, its text, then only
	// spaces.
	lines := strings.Split(strings.TrimSuffix(summary, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "| Parameter        | Value        | Description ") ||
		!strings.HasPrefix(lines[1], "| ---              | ---          | ---  ") ||
		!strings.HasPrefix(lines[3], "| Pool             | fib_adaptive | task pool, used by Go event-loop frameworks only ") {
		t.Errorf("Summary is not three left-aligned columns:\n%s", summary)
	}
	for _, line := range lines {
		for _, cell := range strings.Split(strings.Trim(line, "|"), "|") {
			if text := strings.TrimRight(cell, " "); !strings.HasPrefix(text, " ") || strings.HasPrefix(text, "  ") {
				t.Errorf("cell %q of the Summary is not left-aligned:\n%s", cell, summary)
			}
		}
	}

	if Summary() != "" || Summary(nil, nil) != "" {
		t.Error("Summary of no reports is not empty")
	}
}

// TestSummaryParametersListsEveryTag keeps SummaryParameters, which both
// clients order the Summary table by, in step with the tags.
func TestSummaryParametersListsEveryTag(t *testing.T) {
	listed := map[string]bool{}
	for _, p := range SummaryParameters {
		listed[p.Name] = true
		if p.Description == "" {
			t.Errorf("SummaryParameters gives %v no description", p.Name)
		}
	}
	for _, r := range []interface{}{ConnectionsReport{}, BenchEchoReport{}, BenchRateReport{}} {
		typ := reflect.TypeOf(r)
		for i := 0; i < typ.NumField(); i++ {
			if name := typ.Field(i).Tag.Get("summary"); name != "" && !listed[name] {
				t.Errorf("%v.%v is tagged summary:%q, which SummaryParameters does not list",
					typ.Name(), typ.Field(i).Name, name)
			}
		}
	}
}

// TestSortResultBreaksAnEchoTieByEER ranks two echo runs with the same TPS by
// the CPU they spent on it; Connections, which has no EER, keeps a tie in
// framework order.
func TestSortResultBreaksAnEchoTieByEER(t *testing.T) {
	echo := []Report{
		&BenchEchoReport{Framework: "costly", TPS: 300, EER: 5},
		&BenchEchoReport{Framework: "slow", TPS: 10, EER: 900},
		&BenchEchoReport{Framework: "cheap", TPS: 300, EER: 50},
	}
	if got, want := names(SortReports(echo, SortResult)), []string{"cheap", "costly", "slow"}; !equal(got, want) {
		t.Errorf("BenchEcho ranked %v, want %v", got, want)
	}
	conns := []Report{
		&ConnectionsReport{Framework: "b", TPS: 300},
		&ConnectionsReport{Framework: "a", TPS: 300},
	}
	if got, want := names(SortReports(conns, SortResult)), []string{"b", "a"}; !equal(got, want) {
		t.Errorf("Connections ranked %v, want %v", got, want)
	}
}

// TestMarkdownShowsThePercentOfTheBestEER gives the EER column its own
// percentages, of the best EER rather than of the row ranked first.
func TestMarkdownShowsThePercentOfTheBestEER(t *testing.T) {
	Init(false)
	echo := Markdown([]Report{
		&BenchEchoReport{Framework: "fast", TPS: 3000, EER: 1250.5},
		&BenchEchoReport{Framework: "lean", TPS: 1500, EER: 2501},
		&BenchEchoReport{Framework: "slow", TPS: 10, EER: 9.25},
	}, false, SortResult, nil)
	for _, cell := range []string{"| 1250.50  50% |", "| 2501.00 100% |", "|    9.25   0% |"} {
		if !strings.Contains(echo, cell) {
			t.Errorf("BenchEcho: no %q in:\n%s", cell, echo)
		}
	}
	rate := Markdown([]Report{
		&BenchRateReport{Framework: "a", TPS: 200, EchoEER: 40},
		&BenchRateReport{Framework: "b", TPS: 50, EchoEER: 160},
	}, false, SortFramework, nil)
	for _, cell := range []string{"|  40.00  25% |", "| 160.00 100% |"} {
		if !strings.Contains(rate, cell) {
			t.Errorf("BenchRate: no %q in:\n%s", cell, rate)
		}
	}
	if conns := Markdown([]Report{&ConnectionsReport{Framework: "a", TPS: 5}}, false, SortResult, nil); strings.Count(conns, "%") != 1 {
		t.Errorf("Connections shows a percentage outside TPS:\n%s", conns)
	}
}

// TestMarkdownTableIsPerfsForASCII holds the port to perf's own output on the
// tables every report wrote before the rank markers, cell for cell.
func TestMarkdownTableIsPerfsForASCII(t *testing.T) {
	for _, c := range []struct {
		title []string
		rows  [][]string
	}{
		{[]string{"Framework", "TPS", "EER"}, [][]string{{"fib", "474843 100%", "1650.41"}, {"fasthttp", "4", "1.5"}}},
		{[]string{"Parameter", "Value"}, [][]string{{"Pool", "- (fasthttp); fib_adaptive (fib, fnet)"}, {"Conns", "20000"}}},
		{[]string{"A"}, [][]string{{"a much longer first cell", "extra"}, {}}},
		{[]string{"Framework", "Odd"}, nil},
	} {
		want := perf.NewTable()
		want.SetTitle(append([]string(nil), c.title...))
		for _, row := range c.rows {
			want.AddRow(append([]string(nil), row...))
		}
		if got := markdownTable(c.title, c.rows); got != want.Markdown() {
			t.Errorf("markdownTable differs from perf:\n%s\nwant:\n%s", got, want.Markdown())
		}
	}
}

// TestRankMarkersKeepTheColumnsInLine puts [↓1] and [↓2] on the rank columns'
// titles in either order, and every line of the table at one width, which
// counting the markers' bytes would not.
func TestRankMarkersKeepTheColumnsInLine(t *testing.T) {
	Init(false)
	for _, order := range SortOrders() {
		for _, c := range []struct {
			reports []Report
			markers []string
		}{
			{[]Report{&ConnectionsReport{Framework: "a", TPS: 5}, &ConnectionsReport{Framework: "b", TPS: 50}}, []string{" TPS [↓1] "}},
			{[]Report{&BenchEchoReport{Framework: "a", TPS: 5, EER: 2}, &BenchEchoReport{Framework: "b", TPS: 50, EER: 1}}, []string{" TPS [↓1] ", " EER [↓2] "}},
			{[]Report{&BenchRateReport{Framework: "a", TPS: 5, RecvTimes: 50, EchoEER: 2}}, []string{" TPS [↓1] ", " EER [↓2] "}},
		} {
			table := Markdown(c.reports, false, order, nil)
			title := strings.SplitN(table, "\n", 2)[0]
			for _, marker := range c.markers {
				if !strings.Contains(title, marker) {
					t.Errorf("-sort=%v: no %q in %q", order, marker, title)
				}
			}
			if strings.Count(title, "↓") != len(c.markers) {
				t.Errorf("-sort=%v: %q marks other columns too", order, title)
			}
			lines := strings.Split(strings.TrimSuffix(table, "\n"), "\n")
			for _, line := range lines {
				if utf8.RuneCountInString(line) != utf8.RuneCountInString(lines[0]) {
					t.Errorf("-sort=%v: lines of different widths:\n%s", order, table)
					break
				}
			}
		}
	}
	// The markers go on a copy: the headers the next table reads are plain.
	if strings.Contains(strings.Join(BenchEchoReportMarkdownHeaders, ","), "↓") {
		t.Errorf("Markdown marked the shared headers: %v", BenchEchoReportMarkdownHeaders)
	}
}

// TestRateTPSIsPacketsPerSecond puts BenchRate's TPS right after Framework
// and Lang, works it out for a report written before it had one, and leaves
// Packet Recv a plain column.
func TestRateTPSIsPacketsPerSecond(t *testing.T) {
	Init(false)
	if got := BenchRateReportMarkdownHeaders[:4]; !equal(got, []string{"Framework", "Lang", "TPS", "EER"}) {
		t.Errorf("BenchRate columns start %v, want Framework, Lang, TPS, EER", got)
	}
	if got := RateTPS(39809390, 10e9); got != 3980939 {
		t.Errorf("RateTPS = %v, want 3980939", got)
	}
	if got := RateTPS(10, 0); got != 0 {
		t.Errorf("RateTPS of no duration = %v, want 0", got)
	}

	old := &BenchRateReport{Framework: "fib", RecvTimes: 39809399, Duration: 10e9}
	old.fillTPS()
	if old.TPS != 3980939 {
		t.Errorf("fillTPS set %v, want 3980939", old.TPS)
	}
	recorded := &BenchRateReport{TPS: 7, RecvTimes: 1000, Duration: 1e9}
	recorded.fillTPS()
	if recorded.TPS != 7 {
		t.Errorf("fillTPS replaced a recorded TPS with %v", recorded.TPS)
	}

	table := Markdown([]Report{old}, false, SortResult, nil)
	if !strings.Contains(table, "| 3980939 100% |") || !strings.Contains(table, " 39809399 ") {
		t.Errorf("BenchRate table:\n%s", table)
	}
}

// TestPoolSummaryNamesOnlyThePools keeps the Pool row short: the pools that
// ran, once each, without the frameworks or uwebsockets' side of its loop, and
// "-" when no server installed one.
func TestPoolSummaryNamesOnlyThePools(t *testing.T) {
	for _, c := range []struct {
		pools []string
		want  string
	}{
		{[]string{"fib_adaptive", "-", "fib_adaptive"}, "fib_adaptive"},
		{[]string{"-", "nbio", "nbio(pool)", "ants"}, "nbio, ants"},
		{[]string{"-", "-"}, "-"},
	} {
		var reports []Report
		for i, pool := range c.pools {
			reports = append(reports, &ConnectionsReport{Framework: strconv.Itoa(i), TaskPool: pool})
		}
		if !strings.Contains(Summary(reports), "| Pool             | "+c.want+" ") ||
			!strings.Contains(Summary(reports), " | task pool, used by Go event-loop frameworks only ") {
			t.Errorf("pools %v: want Pool %q in:\n%s", c.pools, c.want, Summary(reports))
		}
	}
}

// TestPercentOfFloats holds Percent to its promises for the float results EER
// is: the best reads 100% against itself, which floor(best*100/best) failed
// for one float in twenty or so, an exact whole percent of it reads that
// percent, and anything under it reads under 100%.
func TestPercentOfFloats(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	for i := 0; i < 1000000; i++ {
		best := random.Float64() * 20000
		if best == 0 {
			continue
		}
		if got := Percent(best, best); got != "100%" {
			t.Fatalf("Percent(%v, itself) = %v", best, got)
		}
		k := 1 + random.Intn(99)
		if got, want := Percent(best*float64(k)/100, best), strconv.Itoa(k)+"%"; got != want {
			t.Fatalf("Percent(%v of %v) = %v, want %v", k, best, got, want)
		}
		if got := Percent(math.Nextafter(best, 0), best); got != "99%" {
			t.Fatalf("Percent(just under %v) = %v, want 99%%", best, got)
		}
	}

	// One of the EERs that used to leave its table without a 100% row.
	Init(false)
	table := Markdown([]Report{
		&BenchEchoReport{Framework: "a", TPS: 10, EER: 1395.7292860139253},
		&BenchEchoReport{Framework: "b", TPS: 20, EER: 697.86464300696265},
	}, false, SortResult, nil)
	for _, cell := range []string{"| 1395.73 100% |", "|  697.86  50% |"} {
		if !strings.Contains(table, cell) {
			t.Errorf("no %q in:\n%s", cell, table)
		}
	}
}

// TestLangFollowsFramework puts the Lang column right after Framework in all
// three tables, and fills it in from the config for a report file written
// before the column existed.
func TestLangFollowsFramework(t *testing.T) {
	Init(false)
	for name, headers := range map[string][]string{
		"Connections": ConnectionsReportMarkdownHeaders,
		"BenchEcho":   BenchEchoReportMarkdownHeaders,
		"BenchRate":   BenchRateReportMarkdownHeaders,
	} {
		if len(headers) < 2 || headers[0] != "Framework" || headers[1] != "Lang" {
			t.Errorf("%s columns start %v, want Framework, Lang", name, headers)
		}
	}

	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	if err := os.MkdirAll("output/report", 0755); err != nil {
		t.Fatal(err)
	}
	// Written the way a client wrote them before the column: no "Lang".
	for _, framework := range []string{config.Gorilla, config.TokioTungstenite, config.Uwebsockets} {
		body := `{"Framework":"` + framework + `","BenchClient":"benchcli-go","TPS":100}`
		if err := os.WriteFile("output/report/"+framework+"-BenchEcho.json", []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{config.Gorilla: "go", config.TokioTungstenite: "rust", config.Uwebsockets: "c++"}
	reports := ReadBenchEchoReports("", "")
	if len(reports) != len(want) {
		t.Fatalf("read %d reports, want %d", len(reports), len(want))
	}
	for _, r := range reports {
		echo := r.(*BenchEchoReport)
		if echo.Lang != want[echo.Framework] {
			t.Errorf("%s: Lang = %q, want %q", echo.Framework, echo.Lang, want[echo.Framework])
		}
	}
	table := Markdown(reports, false, SortFramework, nil)
	for framework, lang := range want {
		if !regexp.MustCompile(`\| *` + framework + ` *\| *` + regexp.QuoteMeta(lang) + ` *\|`).MatchString(table) {
			t.Errorf("no %s row with Lang %s in:\n%s", framework, lang, table)
		}
	}
}
