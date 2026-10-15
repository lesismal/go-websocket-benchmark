package report

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"go-websocket-benchmark/logging"
)

// ClientFlags is the order the Summary's command-line table lists the
// benchmark client's flags in, and what its Description column says of each.
// All three clients take the same flags, so these words are theirs too:
// benchcli-uwscpp and benchcli-rust read this list, as they read
// SummaryParameters. A flag a client has and this list lacks still gets a row,
// after these, with no description.
var ClientFlags = []SummaryParameter{
	{"f", "framework benchmarked; the driver runs the client once per framework"},
	{"ip", "server host the client connects to"},
	{"c", "connections dialed and held for every benchmark"},
	{"dc", "connections dialed at once"},
	{"dt", "dial timeout, TCP connect plus WebSocket upgrade"},
	{"dr", "dial retries per connection"},
	{"dri", "wait between dial retries"},
	{"nodelay", "TCP_NODELAY on the client's connections"},
	{"b", "payload size in bytes, BenchEcho and BenchPipeline"},
	{"check", "check every response's data"},
	{"tpn", "compute latency percentiles (TP50 ... TP99.9)"},
	{"pi", "interval between the server's CPU and MEM samples, in ms"},
	{"ps", "where CPU and MEM samples come from: auto, local (sampled here) or remote (asked over HTTP)"},
	{"m", "client memory limit in bytes (Go: soft limit; native clients: address space on Linux, sampled RSS on macOS; 0 unlimited)"},
	{"ec", "BenchEcho: echo requests in flight at once"},
	{"en", "BenchEcho: echo round trips per framework"},
	{"el", "BenchEcho: TPS limit, 0 unlimited"},
	{"ep", "BenchEcho: sample Go servers' CPU and heap pprof"},
	{"epd", "BenchEcho: pprof CPU profile length, in seconds"},
	{"rate", "run BenchPipeline after BenchEcho"},
	{"rc", "BenchPipeline: connections sending at once"},
	{"rd", "BenchPipeline: how long it sends for, in seconds"},
	{"rr", "BenchPipeline: messages sent to each connection per second, 0 unlimited"},
	{"rbs", "BenchPipeline: most bytes written to a connection at once"},
	{"rpl", "BenchPipeline: messages merged into one write, 0 as many as -rbs holds"},
	{"rl", "BenchPipeline: messages sent per second in all, 0 unlimited"},
	{"rp", "BenchPipeline: sample Go servers' CPU and heap pprof"},
	{"rpd", "BenchPipeline: pprof CPU profile length, in seconds"},
	{"threads", "native clients only: event-loop threads, 0 for the available CPUs, capped by concurrency"},
	{"io-timeout", "native clients only: longest wait for an echo response"},
	{"r", "report step: write these tables from the run's JSON"},
	{"preffix", "report file name prefix"},
	{"suffix", "report file name suffix"},
	{"sort", "report row order: result ranks the best first, framework keeps the framework order"},
	{"project", "the Summary's Project row"},
}

// ReportStepFlags are the flags whose values the report step, not the
// benchmark run, decides: the table shows the report step's own values of
// these, and the run's of every other flag.
var ReportStepFlags = []string{"r", "preffix", "suffix", "sort", "project"}

// ClientFlag is one flag of a client's command line: its name without the
// dash, its value, and whether the command line set it rather than leaving
// the client's default.
type ClientFlag struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
	Set   bool   `json:"Set"`
}

// CommandLine is every flag a benchmark run's client had, in the order the
// client lists them, written to CommandLineFilename beside the reports. Each
// framework's run overwrites it: the driver gives every one of them the same
// flags but -f.
type CommandLine struct {
	Client string       `json:"Client"`
	Flags  []ClientFlag `json:"Flags"`
}

// CommandLineFilename is where a run's CommandLine is, under the same
// -preffix and -suffix as its reports. benchcli-uwscpp and benchcli-rust
// write and read the same file.
func CommandLineFilename(preffix, suffix string) string {
	return Filename("CommandLine", preffix, suffix+".json")
}

// WriteCommandLine records cl for the report step.
func WriteCommandLine(cl CommandLine, preffix, suffix string) error {
	b, err := json.Marshal(cl)
	if err != nil {
		return err
	}
	filename := CommandLineFilename(preffix, suffix)
	if err := os.MkdirAll(filepath.Dir(filename), 0777); err != nil {
		return err
	}
	return os.WriteFile(filename, b, 0666)
}

// ReadCommandLine reads the run's CommandLine. A run whose client predates
// the file has none, and a file that cannot be read is logged and taken as
// none: the Summary is then without the table rather than the report without
// its Summary.
func ReadCommandLine(preffix, suffix string) CommandLine {
	filename := CommandLineFilename(preffix, suffix)
	b, err := os.ReadFile(filename)
	if errors.Is(err, fs.ErrNotExist) {
		return CommandLine{}
	}
	var cl CommandLine
	if err == nil {
		err = json.Unmarshal(b, &cl)
	}
	if err != nil {
		logging.Printf("reading %v failed: %v", filename, err)
		return CommandLine{}
	}
	return cl
}

// CommandLineTable is every flag of the run's client - its value, whether the
// command line set it, and what it is - in ClientFlags order:
//
//	-c | 1000 | command line | connections dialed and held for every benchmark
//
// run is the benchmark run's flags; reportStep the report step's, of which
// only ReportStepFlags are taken. -f differs from one framework's run to the
// next, so its row says so rather than naming the last. A run that recorded
// no flags has no table.
func CommandLineTable(run CommandLine, reportStep []ClientFlag) string {
	if len(run.Flags) == 0 {
		return ""
	}
	flags := map[string]ClientFlag{}
	var names []string
	add := func(f ClientFlag) {
		if _, seen := flags[f.Name]; !seen {
			names = append(names, f.Name)
		}
		flags[f.Name] = f
	}
	for _, f := range run.Flags {
		add(f)
	}
	for _, f := range reportStep {
		if slices.Contains(ReportStepFlags, f.Name) {
			add(f)
		}
	}

	var rows [][]string
	for _, name := range flagOrder(names) {
		f := flags[name]
		value := f.Value
		switch {
		case name == "f":
			value = "one run per framework"
		case value == "":
			value = `""`
		}
		source := "default"
		if f.Set {
			source = "command line"
		}
		rows = append(rows, []string{"-" + name, value, source, flagDescription(name)})
	}
	return markdownTableAligned([]string{"Flag", "Value", "Source", "Description"}, rows, true)
}

func flagDescription(name string) string {
	for _, p := range ClientFlags {
		if p.Name == name {
			return p.Description
		}
	}
	return ""
}

// flagOrder puts names in ClientFlags order, and any it does not list after
// them in the order they were found.
func flagOrder(names []string) []string {
	ordered := make([]string, 0, len(names))
	for _, p := range ClientFlags {
		if slices.Contains(names, p.Name) {
			ordered = append(ordered, p.Name)
		}
	}
	for _, name := range names {
		if !slices.Contains(ordered, name) {
			ordered = append(ordered, name)
		}
	}
	return ordered
}
