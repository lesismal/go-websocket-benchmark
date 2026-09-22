package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-websocket-benchmark/logging"

	"github.com/lesismal/perf"
	"github.com/shirou/gopsutil/process"
)

// ServerProcessName is the name every framework's server binary is built
// under: script/build.sh writes ./output/bin/<framework>.server, and
// script/killone.sh matches the same path. It is what identifies a server
// process on this machine when a client samples it directly.
func ServerProcessName(framework string) string {
	return framework + ".server"
}

// FindServerProcess returns the pid of the framework's server process on this
// machine, found by the name it was built under rather than by asking it.
//
// Two servers answering to one name is an error rather than a guess: that is
// a leftover from an earlier run next to the one being measured, and sampling
// the wrong one would fill the resource columns with a number that has
// nothing to do with the benchmark. So is no match at all, which is what a
// server in another container - or on another machine - looks like from here.
// Both leave the caller to fall back to the server's own sampling.
func FindServerProcess(framework string) (int, error) {
	name := ServerProcessName(framework)
	pids, err := findProcessesByName(name)
	if err != nil {
		return -1, err
	}
	switch len(pids) {
	case 0:
		return -1, fmt.Errorf("no %v process on this machine", name)
	case 1:
		return pids[0], nil
	default:
		return -1, fmt.Errorf("%d %v processes on this machine (%v): stop the leftovers"+
			" of earlier runs, e.g. with script/killall.sh", len(pids), name, pids)
	}
}

// VerifyServerProcess reports whether pid is this framework's server on this
// machine. A pid is only ever as good as the namespace it came from: the one
// /init answers with is the server's own, which is a different process here
// when the server runs in another container, and sampling it would measure
// whatever happens to hold that number locally.
func VerifyServerProcess(pid int, framework string) error {
	name, err := processName(pid)
	if err != nil {
		return err
	}
	if want := ServerProcessName(framework); name != want {
		return fmt.Errorf("pid %d on this machine is %q, not %q", pid, name, want)
	}
	return nil
}

// findProcessesByName lists the pids whose argv[0] has this base name.
func findProcessesByName(name string) ([]int, error) {
	if pids, err := procFSPids(); err == nil {
		matched := []int{}
		for _, pid := range pids {
			// A process that exited between the listing and the read is not
			// an error; it is simply not a match.
			if procName, err := processName(pid); err == nil && procName == name {
				matched = append(matched, pid)
			}
		}
		return matched, nil
	}
	return psPidsByName(name)
}

// processName returns the base name of a pid's argv[0], which for a server
// started by script/server.sh is <framework>.server.
func processName(pid int) (string, error) {
	if _, err := os.Stat("/proc"); err == nil {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
		if err != nil {
			return "", err
		}
		argv0, _, _ := bytes.Cut(data, []byte{0})
		if len(argv0) == 0 {
			return "", fmt.Errorf("pid %d: empty command line", pid)
		}
		return filepath.Base(string(argv0)), nil
	}
	names, err := psNames(strconv.Itoa(pid))
	if err != nil {
		return "", err
	}
	if name, ok := names[pid]; ok {
		return name, nil
	}
	return "", fmt.Errorf("pid %d: no such process", pid)
}

// procFSPids lists the pids under /proc, and fails on a machine without one.
func procFSPids() ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		if pid, err := strconv.Atoi(entry.Name()); err == nil {
			pids = append(pids, pid)
		}
	}
	if len(pids) == 0 {
		return nil, fmt.Errorf("/proc lists no process")
	}
	return pids, nil
}

// psPidsByName is the way there when there is no /proc to read: macOS, where
// ps knows every process' full executable path.
func psPidsByName(name string) ([]int, error) {
	names, err := psNames("")
	if err != nil {
		return nil, err
	}
	matched := []int{}
	for pid, procName := range names {
		if procName == name {
			matched = append(matched, pid)
		}
	}
	return matched, nil
}

// psNames reads pid and executable name from ps, for one pid or, with an
// empty pid, for every process this user can see. One ps for the whole
// listing rather than one per process: on a machine carrying a benchmark,
// forking a few hundred times to name a few hundred processes is not free.
func psNames(pid string) (map[int]string, error) {
	args := []string{"-o", "pid=,comm="}
	if pid == "" {
		args = append(args, "-A")
	} else {
		args = append(args, "-p", pid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	names := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		procPid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		// An executable path with spaces in it comes back in several fields;
		// only the base name is wanted, so rejoin before taking it.
		names[procPid] = filepath.Base(strings.Join(fields[1:], " "))
	}
	return names, nil
}

// LocalPSSampler samples a server process running on this machine, at the
// interval the run's -pi sets, the way the server's own /ps sampler does.
// Nothing is asked of the server, so nothing here fails because it is busy
// carrying a million connections.
type LocalPSSampler struct {
	pid      int
	interval time.Duration
	proc     *process.Process

	// The samples so far, and a lock around them: a report is built while the
	// sampling goroutine is still appending.
	mu  sync.Mutex
	cpu []float64
	mem []*process.MemoryInfoStat

	// Where to read the samples from when this sampler has none of its own -
	// the server's /ps route, when the run asked it to sample as well.
	fallback PSSource

	cancel func()
	done   chan struct{}
}

// consecutiveSampleErrors is where the sampler gives up. The process is gone,
// almost always: a server that exited or was killed mid-run. Whatever was
// sampled before that is kept, since it is the benchmark that was measured.
const consecutiveSampleErrors = 5

// StartLocalPSSampler starts sampling pid in the background, after one read
// that proves the process can be sampled at all. It is the caller's job to
// Stop it.
func StartLocalPSSampler(pid int, interval time.Duration, fallback PSSource) (*LocalPSSampler, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid pid %d", pid)
	}
	if interval <= 0 {
		interval = time.Second
	}
	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		return nil, fmt.Errorf("pid %d: %w", pid, err)
	}
	// Read once before returning, so that a process this client cannot sample
	// at all - gone, or another user's - is a failure here rather than an
	// empty resource column at the end of the run.
	if _, err := proc.MemoryInfo(); err != nil {
		return nil, fmt.Errorf("pid %d: %w", pid, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &LocalPSSampler{
		pid:      pid,
		interval: interval,
		proc:     proc,
		fallback: fallback,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go s.run(ctx)
	return s, nil
}

// Pid is the process being sampled.
func (s *LocalPSSampler) Pid() int { return s.pid }

func (s *LocalPSSampler) String() string {
	return fmt.Sprintf("sampled here, from pid %d", s.pid)
}

func (s *LocalPSSampler) run(ctx context.Context) {
	defer close(s.done)
	errCount := 0
	for {
		// Percent sleeps the interval itself and answers with the CPU used
		// over it, so this loop produces one sample per interval the way the
		// server's own counter does, and stops promptly when ctx is done.
		percent, err := s.proc.PercentWithContext(ctx, s.interval)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			errCount++
			if errCount >= consecutiveSampleErrors {
				logging.Printf("sampling pid %d stopped after %d failures, keeping %d samples: %v",
					s.pid, errCount, s.sampleCount(), err)
				return
			}
			continue
		}
		errCount = 0
		mem, memErr := s.proc.MemoryInfo()
		s.mu.Lock()
		s.cpu = append(s.cpu, percent)
		if memErr == nil {
			s.mem = append(s.mem, mem)
		}
		s.mu.Unlock()
	}
}

func (s *LocalPSSampler) sampleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cpu)
}

// PsInfo returns the samples taken so far, as the counter the reports read
// their CPU and MEM columns from - the same type, and the same statistics,
// that the server's /ps route answers with.
func (s *LocalPSSampler) PsInfo() (*perf.PSCounter, error) {
	s.mu.Lock()
	counter := &perf.PSCounter{}
	counter.RetCPU = append([]float64(nil), s.cpu...)
	counter.RetMEM = append([]*process.MemoryInfoStat(nil), s.mem...)
	s.mu.Unlock()

	if len(counter.RetCPU) > 0 {
		return counter, nil
	}
	// Nothing sampled yet: the phase was shorter than one -pi interval, or
	// the process went away before the first sample. Whatever the server
	// sampled for itself is better than an empty column, when it was asked to.
	if s.fallback != nil {
		return s.fallback.PsInfo()
	}
	return counter, fmt.Errorf("pid %d: no CPU samples yet, so the phase was shorter than"+
		" the -pi sampling interval", s.pid)
}

// Stop ends the sampling and waits for the goroutine to finish, so that no
// sample arrives after a caller has read the last report.
func (s *LocalPSSampler) Stop() {
	s.cancel()
	<-s.done
	if s.fallback != nil {
		s.fallback.Stop()
	}
}
