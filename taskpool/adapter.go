package taskpool

import (
	fibpool "github.com/lesismal/fib/taskpool"
)

// FibTaskPool adapts a Pool to the interface github.com/lesismal/fib takes
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
// runs leaves the connection stuck rather than merely dropping a message.
func NbioExecute(pool Pool) func(f func()) { return runOrCall(pool) }

// FnetWorkerPool adapts a Pool to fnet's websocket.Upgrader.WorkerPool.
//
// What fnet submits is a drain of one connection's queued frames, and it
// submits one only when no drain is in flight, so a task that never runs
// leaves that flag set and every later frame queued behind it for good.
//
// It goes on the upgrader rather than on fnet.Server, whose WorkerPool runs
// the HTTP request loop and holds a worker for as long as a connection stays
// unupgraded: a bounded pool there would wedge on the handshake burst rather
// than measure the callbacks.
func FnetWorkerPool(pool Pool) func(task func()) { return runOrCall(pool) }

// runOrCall is the executor for a framework that takes a func it cannot be
// told was declined. A task the pool refuses runs on the caller's goroutine,
// which is an I/O one: the same thing these frameworks do when they are
// configured with no pool at all, and it keeps the connection's order because
// the task the caller runs is the one it was about to hand over.
func runOrCall(pool Pool) func(f func()) {
	return func(f func()) {
		if !pool.Go(f) {
			call(f)
		}
	}
}
