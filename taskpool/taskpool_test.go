package taskpool

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pooled lists every implementation a server can select, so that a new one
// has to pass the same checks as the rest.
func pooled() []string {
	names := Names()
	return names[1:] // Names puts Default, which builds no pool, first.
}

func newTestPool(t *testing.T, name string) Pool {
	t.Helper()
	pool, err := New(Config{Name: name, MinWorkers: 4, MaxWorkers: 64, QueueSize: 256})
	if err != nil {
		t.Fatalf("New(%q) failed: %v", name, err)
	}
	if pool == nil {
		t.Fatalf("New(%q) returned no pool", name)
	}
	t.Cleanup(pool.Stop)
	return pool
}

func TestNewDefaultBuildsNoPool(t *testing.T) {
	for _, name := range []string{"", Default} {
		pool, err := New(Config{Name: name})
		if err != nil {
			t.Fatalf("New(%q) failed: %v", name, err)
		}
		if pool != nil {
			t.Fatalf("New(%q) built %T, want no pool", name, pool)
		}
	}
}

func TestNewRejectsUnknownName(t *testing.T) {
	if _, err := New(Config{Name: "nosuchpool"}); err == nil {
		t.Fatal("New accepted a name no pool is registered under")
	}
}

func TestNamesCoversEveryFramework(t *testing.T) {
	registered := map[string]bool{}
	for _, name := range Names() {
		registered[name] = true
	}
	for _, name := range []string{Default, Inline, Goroutine, FibAdaptive, FibCond, FibElastic, Nbio, Fnet, Greatws, Uws} {
		if !registered[name] {
			t.Errorf("Names omits %q", name)
		}
	}
}

// TestPoolRunsEveryAcceptedTask is the contract the servers rely on: a task
// the pool accepted runs exactly once, and one it declined does not run at
// all.
func TestPoolRunsEveryAcceptedTask(t *testing.T) {
	for _, name := range pooled() {
		t.Run(name, func(t *testing.T) {
			pool := newTestPool(t, name)

			const tasks = 2000
			var done sync.WaitGroup
			var ran, accepted atomic.Int64
			for range tasks {
				done.Add(1)
				if pool.Go(func() { ran.Add(1); done.Done() }) {
					accepted.Add(1)
					continue
				}
				done.Done()
				if !pool.Rejects() {
					t.Fatalf("%s declined a task but reports Rejects() == false", name)
				}
			}
			waitOrFail(t, &done, "tasks did not finish")

			if got := ran.Load(); got != accepted.Load() {
				t.Errorf("%s ran %d of the %d tasks it accepted", name, got, accepted.Load())
			}
			if !pool.Rejects() && accepted.Load() != tasks {
				t.Errorf("%s declined %d tasks but reports Rejects() == false", name, tasks-accepted.Load())
			}
		})
	}
}

// TestPoolSurvivesPanickingTask checks that one bad task cannot take the
// server down, which matters because two of these pools reach the task
// through an interface that does not recover for them.
func TestPoolSurvivesPanickingTask(t *testing.T) {
	for _, name := range pooled() {
		t.Run(name, func(t *testing.T) {
			pool := newTestPool(t, name)

			var done sync.WaitGroup
			done.Add(1)
			if !pool.Go(func() { defer done.Done(); panic("task panicked on purpose") }) {
				t.Skipf("%s declined the task", name)
			}
			waitOrFail(t, &done, "panicking task did not finish")

			done.Add(1)
			if !pool.Go(done.Done) {
				t.Fatalf("%s declined work after a task panicked", name)
			}
			waitOrFail(t, &done, "pool stopped running tasks after a panic")
		})
	}
}

func TestPoolRunsTasksConcurrently(t *testing.T) {
	for _, name := range pooled() {
		if name == Inline {
			continue // Inline is the one that deliberately does not.
		}
		t.Run(name, func(t *testing.T) {
			pool := newTestPool(t, name)

			// Each task blocks until both have started, so the pair can
			// only finish if the pool runs them on different goroutines.
			var started sync.WaitGroup
			var done sync.WaitGroup
			started.Add(2)
			done.Add(2)
			for range 2 {
				if !pool.Go(func() {
					defer done.Done()
					started.Done()
					started.Wait()
				}) {
					t.Fatalf("%s declined a task", name)
				}
			}
			waitOrFail(t, &done, "pool ran the tasks one after the other")
		})
	}
}

type refusingPool struct{}

func (refusingPool) Go(func()) bool { return false }
func (refusingPool) Workers() int   { return 0 }
func (refusingPool) Rejects() bool  { return true }
func (refusingPool) Stop()          {}

func waitOrFail(t *testing.T, wg *sync.WaitGroup, message string) {
	t.Helper()
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal(message)
	}
}
