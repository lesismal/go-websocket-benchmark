package taskpool

import (
	"context"
	"errors"
	"sync"

	"github.com/antlabs/greatws/task/driver"

	"go-websocket-benchmark/logging"
)

// GreatwsTaskName is the task mode RegisterGreatwsTaskDriver publishes under.
// greatws picks a driver by name, and "benchmark" is not one of its own.
const GreatwsTaskName = "benchmark"

var errGreatwsRejected = errors.New("taskpool: pool refused the task")

// RegisterGreatwsTaskDriver publishes pool to greatws's task-driver registry
// and returns the name to hand greatws.WithServerCustomTaskMode, so that a
// greatws server can run its callbacks on any pool here.
//
// greatws instantiates every registered driver once per event loop, so this
// has to be called before the MultiEventLoop is created. It may only be
// called once, since greatws panics on a duplicate driver name.
func RegisterGreatwsTaskDriver(pool Pool) string {
	driver.Register(GreatwsTaskName, &greatwsTaskDriver{pool: pool})
	return GreatwsTaskName
}

// greatwsTaskDriver is its own Tasker: every event loop shares the one pool,
// where greatws's own drivers give each loop a pool of its own.
type greatwsTaskDriver struct{ pool Pool }

func (d *greatwsTaskDriver) New(context.Context, int, int, int, *driver.Conf) driver.Tasker {
	return d
}

func (d *greatwsTaskDriver) GetGoroutines() int { return d.pool.Workers() }

func (d *greatwsTaskDriver) NewExecutor() driver.TaskExecutor {
	return &greatwsTaskExecutor{pool: d.pool}
}

// greatwsTaskExecutor is one connection's queue. greatws hands it that
// connection's own mutex and expects the callbacks to run in the order they
// were added, so the executor keeps at most one drain in flight and lets the
// drain pick up whatever arrived while it was running.
type greatwsTaskExecutor struct {
	pool    Pool
	pending []func() bool
	running bool
	closed  bool
}

func (e *greatwsTaskExecutor) AddTask(mu *sync.Mutex, f func() bool) error {
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
	// carry rather than dropping it, and report the refusal so that greatws
	// logs it.
	lock(mu)
	e.running = false
	unlock(mu)
	return errGreatwsRejected
}

func (e *greatwsTaskExecutor) drain(mu *sync.Mutex) {
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

func (e *greatwsTaskExecutor) Close(mu *sync.Mutex) error {
	lock(mu)
	e.closed = true
	e.pending = nil
	unlock(mu)
	return nil
}

// greatws passes a nil mutex when it closes an executor.
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
			logging.Printf("taskpool: greatws task panicked: %v\n%s", recovered, stack())
		}
	}()
	f()
}
