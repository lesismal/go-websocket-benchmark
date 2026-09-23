package config

import (
	"fmt"
	"os"
	"regexp"
	"testing"
)

// The servers that are not written in Go cannot read Ports, so each carries
// its port range as a pair of constants of its own. A range that drifted from
// the one here would leave the clients dialing ports nothing listens on, and
// the whole framework reporting failures: this holds the copies to Ports.
func TestNativeServersListenOnTheirPorts(t *testing.T) {
	servers := []struct {
		framework string
		source    string
		first     *regexp.Regexp
		last      *regexp.Regexp
	}{
		{
			framework: SockudoWs,
			source:    "../frameworks/sockudo_ws/src/main.rs",
			first:     regexp.MustCompile(`const PORT_START: u16 = (\d+);`),
			last:      regexp.MustCompile(`const PORT_END: u16 = (\d+);`),
		},
		{
			framework: Uwebsockets,
			source:    "../frameworks/uwebsockets/server.cpp",
			first:     regexp.MustCompile(`constexpr int kPortStart = (\d+);`),
			last:      regexp.MustCompile(`constexpr int kPortEnd = (\d+);`),
		},
	}
	for _, server := range servers {
		code, err := os.ReadFile(server.source)
		if err != nil {
			t.Fatalf("reading %s: %v", server.source, err)
		}
		first := server.first.FindSubmatch(code)
		last := server.last.FindSubmatch(code)
		if first == nil || last == nil {
			t.Errorf("found no port constants in %s; have they moved?", server.source)
			continue
		}
		if got, want := fmt.Sprintf("%s:%s", first[1], last[1]), Ports[server.framework]; got != want {
			t.Errorf("%s listens on %s, but Ports[%s] is %s", server.source, got, server.framework, want)
		}
	}
}
