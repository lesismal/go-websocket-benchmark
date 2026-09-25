package config

import (
	"fmt"
	"net"
	"strings"
	"time"

	"go-websocket-benchmark/logging"

	"github.com/lesismal/perf"
)

// Where a report's CPU and MEM columns - and so CPU EER and MEM EER - come from.
//
// There are two ways to sample a server, and the difference is which machine
// does it. Asking the server over its /ps route is the only way when it is on
// another machine, and it is also a request that has to arrive while the
// server is buried under a hundred thousand connections it has just finished
// echoing to: that is precisely when it is most likely to be reset or
// answered too late, and the columns that silently read 0 when it was took
// EER down with them. A run whose server is on this machine does not need the
// request at all - the client can read the process' own CPU and memory
// straight from the operating system, which no amount of load on the server
// can make fail.
const (
	// PSModeAuto samples the server here when it is running on this machine,
	// and asks it over HTTP when it is not. The default.
	PSModeAuto = "auto"
	// PSModeLocal samples here, falling back to the server's own sampling
	// when its process cannot be found on this machine.
	PSModeLocal = "local"
	// PSModeRemote always asks the server, which is what every run did before
	// local sampling existed.
	PSModeRemote = "remote"
)

// ValidatePSMode checks a -ps value, so that a misspelled one fails before a
// run spends the whole benchmark rather than after.
func ValidatePSMode(mode string) error {
	switch mode {
	case PSModeAuto, PSModeLocal, PSModeRemote:
		return nil
	}
	return fmt.Errorf("unsupported -ps value %q (want %v, %v or %v)",
		mode, PSModeAuto, PSModeLocal, PSModeRemote)
}

// PSSource supplies a server's CPU and memory samples when a report is built.
type PSSource interface {
	// PsInfo returns the samples taken so far. It returns the counter it
	// managed to read alongside an error as well as instead of one, so that
	// samples which did arrive are still reported: an error here means the
	// resource columns are incomplete, not that they are all missing.
	PsInfo() (*perf.PSCounter, error)
	// Stop releases whatever the source holds.
	Stop()
	// String says where the numbers came from, for the run's log.
	String() string
}

// RemotePSSource reads the samples the server took of itself, over its /ps
// route. It needs the server to have been sent /init first.
type RemotePSSource struct {
	Framework string
	IP        string
}

func (r *RemotePSSource) PsInfo() (*perf.PSCounter, error) {
	return GetFrameworkPsInfo(r.Framework, r.IP)
}

func (r *RemotePSSource) Stop() {}

func (r *RemotePSSource) String() string {
	return fmt.Sprintf("sampled by the server, read from %v over /ps", r.IP)
}

// PSSetup is what a client needs once it has settled how the server's
// resources will be sampled: the source to build reports from, the server's
// pid, and the address its pprof routes are on.
type PSSetup struct {
	Source    PSSource
	ServerPid int
	PprofAddr string
}

// SetupPS decides how this run reads the server's CPU and memory, and starts
// doing it.
//
// Local sampling asks the server nothing at all: the process is found on this
// machine by the name it was built under, and sampled from here for the rest
// of the run. That is one less thing to fail at the far end of a benchmark
// carrying a million connections, and it is also what /init existed for, so a
// server sampling locally is not asked to sample itself.
//
// Everything that can go wrong with that ends with the server sampling
// itself, as it always has: no matching process, two of them, or a pid that
// cannot be read. The returned setup is usable whatever happened; the error
// says what went wrong on the way, and the caller decides how loudly to say
// so.
func SetupPS(framework, ip, mode string, interval time.Duration) (*PSSetup, error) {
	// The server's own sampling to begin with, so that a setup that gives up
	// anywhere below still hands the caller something to read a report from.
	remote := &RemotePSSource{Framework: framework, IP: ip}
	setup := &PSSetup{Source: remote, ServerPid: -1}
	pprofAddr, err := FrameworkControlAddr(framework, ip)
	if err != nil {
		return setup, err
	}
	setup.PprofAddr = pprofAddr

	wantLocal := mode == PSModeLocal || (mode == PSModeAuto && IsLocalHost(ip))
	if wantLocal {
		pid, findErr := FindServerProcess(framework)
		if findErr == nil {
			sampler, startErr := StartLocalPSSampler(pid, interval, nil)
			if startErr == nil {
				setup.Source = sampler
				setup.ServerPid = pid
				logging.Printf("%v: %v, so it is not asked to sample itself", framework, sampler)
				return setup, nil
			}
			findErr = startErr
		}
		logging.Printf("%v: cannot sample the server from this machine, asking it over HTTP"+
			" instead: %v", framework, findErr)
	}

	// The server samples itself: /init starts that and answers with its pid.
	pid, _, initErr := InitAndGetFrameworkPid(framework, ip, &InitArgs{PsInterval: interval})
	if initErr != nil {
		return setup, initErr
	}
	setup.ServerPid = pid

	// The pid the server just gave us is from its own namespace, so it names
	// this framework's server here only when the two share one. Where it
	// does, sample it from here as well and keep /ps as the fallback: the
	// request has already succeeded once, and the numbers no longer depend on
	// it succeeding again at the end of the run.
	if wantLocal {
		if verifyErr := VerifyServerProcess(pid, framework); verifyErr == nil {
			if sampler, startErr := StartLocalPSSampler(pid, interval, remote); startErr == nil {
				setup.Source = sampler
				logging.Printf("%v: %v, with the server's own /ps as the fallback",
					framework, sampler)
			}
		}
	}
	return setup, nil
}

// IsLocalHost reports whether a host the clients dial is this machine: the
// loopback address, or one of this machine's own interface addresses. It is
// what tells a single-node run from a two-node one without either having to
// say so, since BENCH_SERVER_HOST is the only thing that differs between them.
//
// A host that is this machine's by address may still be another container's
// server, with its own process table; that is why sampling it is attempted
// rather than assumed, and falls back to the server's own sampling.
func IsLocalHost(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil {
			return false
		}
		ips = resolved
	}

	var local map[string]bool
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsUnspecified() {
			return true
		}
		if local == nil {
			local = localAddrs()
		}
		if local[ip.String()] {
			return true
		}
	}
	return false
}

// localAddrs is the set of addresses this machine's interfaces carry.
func localAddrs() map[string]bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	local := make(map[string]bool, len(addrs))
	for _, addr := range addrs {
		switch a := addr.(type) {
		case *net.IPNet:
			local[a.IP.String()] = true
		case *net.IPAddr:
			local[a.IP.String()] = true
		}
	}
	return local
}
