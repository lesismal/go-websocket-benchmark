package config

import (
	"os"
	"regexp"
	"slices"
	"sort"
	"testing"
)

// Every framework list is kept in framework-name order - here, in
// script/config.sh and in script/1m_conns_benchmark.sh - so that a framework
// is in the same place in all of them. FrameworkList is also the row order of
// a -sort=framework report, so this is what a diff between two reports lines
// up on.
func TestFrameworkListSortedByName(t *testing.T) {
	if !sort.StringsAreSorted(FrameworkList) {
		t.Errorf("FrameworkList is not in framework-name order: %v", FrameworkList)
	}
}

// A framework with no ports is one the clients cannot reach, and a port range
// with no framework is one nothing runs on: the two lists have to carry the
// same names.
func TestFrameworkListCoversPorts(t *testing.T) {
	if len(FrameworkList) != len(Ports) {
		t.Errorf("FrameworkList has %d frameworks, Ports has %d", len(FrameworkList), len(Ports))
	}
	for _, framework := range FrameworkList {
		if _, ok := Ports[framework]; !ok {
			t.Errorf("%v has no port range", framework)
		}
	}
	listed := make(map[string]bool, len(FrameworkList))
	for _, framework := range FrameworkList {
		listed[framework] = true
	}
	for framework := range Ports {
		if !listed[framework] {
			t.Errorf("%v has a port range but is not in FrameworkList", framework)
		}
	}
}

// Every framework needs a language for the reports' Lang column, and a
// language entry for a framework that is not in the list would never show.
func TestFrameworkListCoversLangs(t *testing.T) {
	if len(FrameworkList) != len(Langs) {
		t.Errorf("FrameworkList has %d frameworks, Langs has %d", len(FrameworkList), len(Langs))
	}
	for _, framework := range FrameworkList {
		switch lang := Langs[framework]; lang {
		case LangCPP, LangGo, LangRust:
		case "":
			t.Errorf("%v has no language", framework)
		default:
			t.Errorf("%v has language %q, which is none of the Lang constants", framework, lang)
		}
	}
}

// Two frameworks sharing a port could not both be up, and every server in a
// run is started before the first client.
func TestPortsDoNotOverlap(t *testing.T) {
	owner := map[int]string{}
	for _, framework := range FrameworkList {
		ports, err := GetFrameworkBenchmarkPorts(framework)
		if err != nil {
			t.Fatalf("%v: %v", framework, err)
		}
		control, err := frameworkControlPort(framework)
		if err != nil {
			t.Fatalf("%v: %v", framework, err)
		}
		for _, port := range append(ports, control) {
			if other, taken := owner[port]; taken && other != framework {
				t.Errorf("port %d is both %v's and %v's", port, other, framework)
			}
			owner[port] = framework
		}
	}
}

// benchcli-rust and benchcli-uwscpp cannot call frameworkControlPort, so each
// carries its own list of the frameworks whose control routes are on the port
// after their benchmark ones. A list that drifted would send /init, /ps and
// /taskpool to a port nothing answers on.
func TestNativeClientsAgreeOnControlPorts(t *testing.T) {
	var want []string
	for _, framework := range FrameworkList {
		ports, _ := GetFrameworkBenchmarkPorts(framework)
		if control, _ := frameworkControlPort(framework); control != ports[len(ports)-1] {
			want = append(want, framework)
		}
	}
	for _, client := range []struct {
		source string
		list   *regexp.Regexp
	}{
		{"../benchcli-rust/src/http.rs", regexp.MustCompile(`matches!\(f, ((?:"[^"]+"(?: \| )?)+)\)`)},
		{"../benchcli-uwscpp/report.hpp", regexp.MustCompile(`if \(((?:f=="[^"]+"(?: \|\| )?)+)\) \+\+port;`)},
	} {
		code, err := os.ReadFile(client.source)
		if err != nil {
			t.Fatalf("reading %s: %v", client.source, err)
		}
		match := client.list.FindSubmatch(code)
		if match == nil {
			t.Errorf("found no control port list in %s; has it moved?", client.source)
			continue
		}
		var got []string
		for _, name := range regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(match[1], -1) {
			got = append(got, string(name[1]))
		}
		sort.Strings(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s moves the control port of %v, config.frameworkControlPort of %v", client.source, got, want)
		}
	}
}

// BENCH_PORTS narrows the ports the clients dial to the first N of each range
// and leaves the control port where it is; a server whose control routes are
// on the last port of its range keeps listening there.
func TestBenchPorts(t *testing.T) {
	t.Setenv(BenchPortsEnv, "1")
	ports, err := GetFrameworkBenchmarkPorts(Fib)
	if err != nil || len(ports) != 1 || ports[0] != 15101 {
		t.Fatalf("BENCH_PORTS=1: fib ports %v, %v; want [15101]", ports, err)
	}
	if addrs, _ := GetFrameworkServerAddrs(Fib); !slices.Equal(addrs, []string{":15101"}) {
		t.Errorf("BENCH_PORTS=1: fib listens on %v, want [:15101]", addrs)
	}
	if addr, _ := GetFrameworkHTTPServerAddrs(Fib); addr != ":15151" {
		t.Errorf("BENCH_PORTS=1: fib control %v, want :15151", addr)
	}
	if addrs, _ := GetFrameworkServerAddrs(Gorilla); !slices.Equal(addrs, []string{":15601", ":15650"}) {
		t.Errorf("BENCH_PORTS=1: gorilla listens on %v, want [:15601 :15650]", addrs)
	}
	if port, _ := frameworkControlPort(Gorilla); port != 15650 {
		t.Errorf("BENCH_PORTS=1: gorilla control %v, want 15650", port)
	}
	if addrs, _ := GetFrameworkBenchmarkAddrs(Alos, "127.0.0.1"); !slices.Equal(addrs, []string{"ws://127.0.0.1:17801/ws"}) {
		t.Errorf("BENCH_PORTS=1: alos dials %v", addrs)
	}
	for _, v := range []string{"", "0", "-3", "x", "100"} {
		t.Setenv(BenchPortsEnv, v)
		if ports, _ := GetFrameworkBenchmarkPorts(Fib); len(ports) != 50 {
			t.Errorf("BENCH_PORTS=%q: %d ports, want all 50", v, len(ports))
		}
		if addrs, _ := GetFrameworkServerAddrs(Gorilla); len(addrs) != 50 {
			t.Errorf("BENCH_PORTS=%q: gorilla listens on %d ports, want all 50", v, len(addrs))
		}
	}
}
