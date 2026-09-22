package report

import (
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

// TestSortResultRanksRateByBytesReceived holds the rate benchmark to the bytes
// the clients read back, not to what they sent and not to the packet count: a
// server that answered fewer, bigger messages did more work than one that
// answered more, smaller ones.
func TestSortResultRanksRateByBytesReceived(t *testing.T) {
	rate := []Report{
		&BenchRateReport{Framework: "small", SendBytes: 9000, RecvTimes: 900, RecvBytes: 100},
		&BenchRateReport{Framework: "big", SendBytes: 10, RecvTimes: 1, RecvBytes: 9000},
		&BenchRateReport{Framework: "mid", SendBytes: 5000, RecvTimes: 500, RecvBytes: 500},
	}
	if got := names(SortReports(rate, SortResult)); !equal(got, []string{"big", "mid", "small"}) {
		t.Errorf("BenchRate ranked %v, want big, mid, small", got)
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
