package taskpool

import (
	"context"
	"errors"
	"sync"

	"github.com/antlabs/task/task/driver"

	"go-websocket-benchmark/logging"
)

// FioTaskName is the task mode RegisterFioTaskDriver publishes under.
// fio picks a driver by name, and "benchmark" is not one of its own.
const FioTaskName = "benchmark"

var errFioRejected = errors.New("taskpool: pool refused the task")

// RegisterFioTaskDriver publishes pool to fio's task-driver registry
// and returns the name to hand fio.WithServerCustomTaskMode, so that a
// fio server can run its callbacks on any pool here.
//
// fio instantiates every registered driver once per event loop, so this
// has to be called before the MultiEventLoop is created. It may only be
// called once, since fio panics on a duplicate driver name.
func RegisterFioTaskDriver(pool Pool) string {
	driver.Register(FioTaskName, &fioTaskDriver{pool: pool})
	return FioTaskName
}

// fioTaskDriver is its own Tasker: every event loop shares the one pool,
// where fio's own drivers give each loop a pool of its own.
type fioTaskDriver struct{ pool Pool }

func (d *fioTaskDriver) New(context.Context, int, int, int, *driver.Conf) driver.Tasker {
	return d
}

func (d *fioTaskDriver) GetGoroutines() int { return d.pool.Workers() }

func (d *fioTaskDriver) NewExecutor() driver.TaskExecutor {
	return &fioTaskExecutor{pool: d.pool}
}

// fioTaskExecutor is one connection's queue. fio hands it that
// connection's own mutex and expects the callbacks to run in the order they
// were added, so the executor keeps at most one drain in flight and lets the
// drain pick up whatever arrived while it was running.
type fioTaskExecutor struct {
	pool    Pool
	pending []func() bool
	running bool
	closed  bool
}

func (e *fioTaskExecutor) AddTask(mu *sync.Mutex, f func() bool) error {
	lock(mu)
	if e.closed {
		unlock(mu)
		return nil
	}
	e.pending = append(e.pending, f)
	if e.running {
		unlock(mu)
		return nil
	}
	e.running = true
	unlock(mu)

	if e.pool.Go(func() { e.drain(mu) }) {
		return nil
	}
	// Nothing is draining, so leave the task queued for the next AddTask to
	// carry rather than dropping it, and report the refusal so that fio
	// logs it.
	lock(mu)
	e.running = false
	unlock(mu)
	return errFioRejected
}

func (e *fioTaskExecutor) drain(mu *sync.Mutex) {
	// The emptied batch becomes the next one's buffer; see Serial.drain for
	// why it stays a local rather than a field.
	var spare []func() bool
	for {
		lock(mu)
		if e.closed || len(e.pending) == 0 {
			e.running = false
			unlock(mu)
			return
		}
		batch := e.pending
		e.pending = spare[:0]
		unlock(mu)

		for i, task := range batch {
			batch[i] = nil
			callTask(task)
		}
		spare = batch[:0]
	}
}

func (e *fioTaskExecutor) Close(mu *sync.Mutex) error {
	lock(mu)
	e.closed = true
	e.pending = nil
	unlock(mu)
	return nil
}

// fio passes a nil mutex when it closes an executor.
func lock(mu *sync.Mutex) {
	if mu != nil {
		mu.Lock()
	}
}

func unlock(mu *sync.Mutex) {
	if mu != nil {
		mu.Unlock()
	}
}

func callTask(f func() bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logging.Printf("taskpool: fio task panicked: %v\n%s", recovered, stack())
		}
	}()
	f()
}
