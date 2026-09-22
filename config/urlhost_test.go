package config

import "testing"

// BENCH_SERVER_HOST may be a hostname, an IPv4 address or an IPv6 one; only
// the last needs brackets before a port can follow it in a URL.
func TestURLHostBracketsIPv6(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1":      "127.0.0.1",
		"bench-server-1": "bench-server-1",
		"10.0.0.2":       "10.0.0.2",
		"::1":            "[::1]",
		"fe80::1":        "[fe80::1]",
		"[fe80::1]":      "[fe80::1]",
	} {
		if got := urlHost(in); got != want {
			t.Errorf("urlHost(%q) = %q, want %q", in, got, want)
		}
	}
}
