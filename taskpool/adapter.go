package taskpool

import (
	fibpool "github.com/lesismal/fib/go/taskpool"
)

// FibTaskPool adapts a Pool to the interface github.com/lesismal/fib/go takes
// in Config.SetTaskPool.
//
// fib closes the connections behind the tasks its pool declines, so a pool
// whose Rejects reports true drops connections under load where the others
// queue. That is the pool's own character rather than a fault in the
// adapter, and it is what the pool would do to uws as well.
type FibTaskPool struct{ Pool Pool }

func (a FibTaskPool) GoTask(task fibpool.Task) bool { return a.Pool.Go(task.RunTask) }

func (a FibTaskPool) GoTasks(tasks []fibpool.Task) int {
	for i, task := range tasks {
		if !a.Pool.Go(task.RunTask) {
			return i
		}
	}
	return len(tasks)
}

// UwsExecutor adapts a Pool to the uws.Executor interface, which uws uses to
// run its callbacks off the event loop.
type UwsExecutor struct{ Pool Pool }

func (e UwsExecutor) Submit(f func()) bool { return e.Pool.Go(f) }

// NbioExecute adapts a Pool to nbhttp.Config.ServerExecutor.
//
// nbio drives its HTTP parser through that executor, so a step that never
// runs leaves the connection stuck rather than merely dropping a message. A
// task the pool refuses therefore runs on the caller's goroutine, which is
// the poller: the same thing nbio does when it is configured with no pool at
// all.
func NbioExecute(pool Pool) func(f func()) {
	return func(f func()) {
		if !pool.Go(f) {
			call(f)
		}
	}
}
