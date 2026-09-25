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

// An inline entry is its framework's server under another name, so it has to
// be everything the framework is to the clients - listed, reachable, asked for
// its pool - on ports of its own.
func TestInlinesAreFrameworksOfTheirOwn(t *testing.T) {
	for framework, inline := range Inlines {
		if inline != framework+InlineSuffix {
			t.Errorf("%v's inline entry is %v, want %v", framework, inline, framework+InlineSuffix)
		}
		if Langs[inline] != Langs[framework] {
			t.Errorf("%v is %v, but its framework %v is %v: they have to be one server", inline, Langs[inline], framework, Langs[framework])
		}
		for _, name := range []string{framework, inline} {
			if !slices.Contains(FrameworkList, name) {
				t.Errorf("%v is not in FrameworkList", name)
			}
			if !FrameworkHasTaskPool(name) {
				t.Errorf("%v is not in TaskPoolFrameworks", name)
			}
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
