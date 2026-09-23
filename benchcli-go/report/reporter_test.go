package report

import (
	"reflect"
	"strings"
	"testing"
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

// TestSortResultRanksRateByPacketsThenEER holds the rate benchmark to the
// packets the clients read back, not to what they sent or to the bytes, and
// breaks a tie on those by EER.
func TestSortResultRanksRateByPacketsThenEER(t *testing.T) {
	rate := []Report{
		&BenchRateReport{Framework: "few", SendBytes: 9000, RecvTimes: 100, RecvBytes: 9000, EchoEER: 900},
		&BenchRateReport{Framework: "many-costly", RecvTimes: 900, RecvBytes: 10, EchoEER: 5},
		&BenchRateReport{Framework: "mid", RecvTimes: 500, EchoEER: 1},
		&BenchRateReport{Framework: "many-cheap", RecvTimes: 900, RecvBytes: 10, EchoEER: 50},
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
	if table := Markdown([]Report{rate}, true, SortFramework, nil); !strings.Contains(table, " EER ") ||
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
		&BenchRateReport{Framework: "a", RecvTimes: 200, EchoEER: 1},
		&BenchRateReport{Framework: "b", RecvTimes: 50, EchoEER: 9},
	}, false, SortResult, nil)
	// Packet Recv is wider than its cells, so the table centres them; the
	// cells themselves are one width, which keeps the percentages aligned.
	for _, cell := range []string{" 200 100% ", "  50  25% "} {
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
		&BenchRateReport{Framework: "fib", BenchClient: "benchcli-uwscpp", TaskPool: "fib_adaptive", Duration: 10e9, Connections: 20000, Concurrency: 5000, SendRate: 200, Payload: 1024},
	}
	summary := Summary(conns, echo, rate)
	rows := []string{"Client", "uwscpp", "Pool", "fib_adaptive (fib, fnet); - (fasthttp)", "Conns", "20000",
		"Payload", "1024", "Dial Concurrency", "2000", "Echo Concurrency", "10000", "Echo Total", "2000000",
		"Rate Concurrency", "5000", "Rate Duration", "10.00s", "Rate SendRate", "200"}
	if !rowOrder(summary, rows...) {
		t.Errorf("Summary does not read %v:\n%s", rows, summary)
	}

	for _, table := range []string{Markdown(conns, false, SortResult, nil), Markdown(echo, false, SortResult, nil),
		Markdown(rate, false, SortResult, nil)} {
		for _, column := range []string{"Client", "Pool", "Conns", "Concurrency", "Payload", "Duration", "SendRate"} {
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

	if Summary() != "" || Summary(nil, nil) != "" {
		t.Error("Summary of no reports is not empty")
	}
}

// TestSummaryParametersListsEveryTag keeps SummaryParameters, which both
// clients order the Summary table by, in step with the tags.
func TestSummaryParametersListsEveryTag(t *testing.T) {
	listed := map[string]bool{}
	for _, name := range SummaryParameters {
		listed[name] = true
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
