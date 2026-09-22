package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidatePSMode(t *testing.T) {
	for _, mode := range []string{PSModeAuto, PSModeLocal, PSModeRemote} {
		if err := ValidatePSMode(mode); err != nil {
			t.Errorf("ValidatePSMode(%q) = %v, want nil", mode, err)
		}
	}
	for _, mode := range []string{"", "Local", "http", "yes"} {
		if err := ValidatePSMode(mode); err == nil {
			t.Errorf("ValidatePSMode(%q) = nil, want an error", mode)
		}
	}
}

// A single-node run is the one whose BENCH_SERVER_HOST is this machine, which
// is what decides whether the client can sample the server itself.
func TestIsLocalHost(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "[::1]", "localhost", "0.0.0.0"} {
		if !IsLocalHost(host) {
			t.Errorf("IsLocalHost(%q) = false, want true", host)
		}
	}
	// Documentation addresses (RFC 5737 and RFC 3849), which no interface of
	// this machine carries.
	for _, host := range []string{"", "192.0.2.10", "198.51.100.7", "2001:db8::1"} {
		if IsLocalHost(host) {
			t.Errorf("IsLocalHost(%q) = true, want false", host)
		}
	}
}

// Whatever names a server process here, it has to be the name script/build.sh
// builds it under and script/killone.sh stops it by.
func TestServerProcessName(t *testing.T) {
	if got, want := ServerProcessName(Gorilla), "gorilla.server"; got != want {
		t.Errorf("ServerProcessName(%q) = %q, want %q", Gorilla, got, want)
	}
}

// The test binary is the one process this test is sure about, so it is what
// process naming is checked against.
func TestProcessName(t *testing.T) {
	want := filepath.Base(os.Args[0])
	got, err := processName(os.Getpid())
	if err != nil {
		t.Fatalf("processName(%d) failed: %v", os.Getpid(), err)
	}
	if got != want {
		t.Errorf("processName(%d) = %q, want %q", os.Getpid(), got, want)
	}
}

// A framework whose server is not running is what a client sees when the
// server is on the other node of a two-node run, and it has to say so rather
// than answer with some other process.
func TestFindServerProcessMissing(t *testing.T) {
	if pid, err := FindServerProcess("no-such-framework-" + t.Name()); err == nil {
		t.Errorf("FindServerProcess found pid %d, want an error", pid)
	}
}

// A pid that is not this framework's server must be refused, since the pid
// /init answers with is only this machine's when the server shares its
// process table.
func TestVerifyServerProcess(t *testing.T) {
	if err := VerifyServerProcess(os.Getpid(), Gorilla); err == nil {
		t.Errorf("VerifyServerProcess(%d, %q) = nil, want an error", os.Getpid(), Gorilla)
	}
}

// The sampler is pointed at this test's own process: it has to produce the
// samples a report reads, and stop when it is told to.
func TestLocalPSSampler(t *testing.T) {
	sampler, err := StartLocalPSSampler(os.Getpid(), 50*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("StartLocalPSSampler failed: %v", err)
	}
	defer sampler.Stop()

	if sampler.Pid() != os.Getpid() {
		t.Errorf("Pid() = %d, want %d", sampler.Pid(), os.Getpid())
	}

	// Before the first interval has passed there is nothing to report, and
	// the sampler says so rather than letting the columns read 0 quietly.
	counter, err := sampler.PsInfo()
	if err == nil {
		t.Errorf("PsInfo() before the first sample = %v, want an error", err)
	}
	if counter == nil {
		t.Fatal("PsInfo() returned no counter; samples that did arrive must still be reported")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		counter, err = sampler.PsInfo()
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("PsInfo() kept failing: %v", err)
	}
	if len(counter.RetCPU) == 0 {
		t.Error("PsInfo() returned no CPU samples")
	}
	if counter.MEMRSSMax() == 0 {
		t.Error("PsInfo() returned no memory samples")
	}
}

// An invalid pid must fail where the run can still fall back to the server's
// own sampling, rather than at the end of the benchmark with empty columns.
func TestLocalPSSamplerInvalidPid(t *testing.T) {
	if sampler, err := StartLocalPSSampler(0, time.Second, nil); err == nil {
		sampler.Stop()
		t.Error("StartLocalPSSampler(0) = nil error, want a failure")
	}
}
