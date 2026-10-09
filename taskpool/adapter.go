package taskpool

import (
	fibpool "github.com/lesismal/fib/taskpool"
	"github.com/urpc/uio"
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

// UwsExecutor adapts a Pool to uio.Executor, which UIO uses to run each
// connection's I/O task off its event loops.
//
// UIO closes the connection behind a task the executor refuses, so a pool
// whose Rejects reports true drops connections under load, as it does under
// fib. A batch stops at the first refusal: UIO takes the accepted prefix and
// closes the connections behind the rest.
type UwsExecutor struct{ Pool Pool }

func (e UwsExecutor) Submit(task uio.IOTask) bool { return e.Pool.Go(task.RunTask) }

func (e UwsExecutor) SubmitBatch(tasks []uio.IOTask) int {
	for i, task := range tasks {
		if !e.Pool.Go(task.RunTask) {
			return i
		}
	}
	return len(tasks)
}

// NbioExecute adapts a Pool to nbhttp.Config.ServerExecutor.
//
// nbio drives its HTTP parser through that executor, so a step that never
// runs leaves the connection stuck rather than merely dropping a message.
func NbioExecute(pool Pool) func(f func()) { return runOrCall(pool) }

// FnetExecutor adapts a Pool to fnet.Options.Executor.
//
// What fnet submits is a connection's task: it reads, runs the callbacks,
// flushes the send buffer and closes. It submits one only when the connection
// has none in flight, so a task that never runs leaves the connection silent
// for good, and an Executor has no way to report a refusal. A task the pool
// refuses therefore runs on a goroutine of its own, as it does in fnet's pool
// when a shard's queue is full, and order is kept because the connection has
// no other task to overtake it.
//
// It does not use runOrCall: fnet calls its Executor from the event loops,
// from other connections' callbacks and from a task on its way out, and
// requires that the task never run on the caller's stack. That is also why the
// fnet server refuses the Inline pool.
//
// fnet passes a key with each task, the same for all of a connection's tasks,
// so that its own pool keeps them on one shard. The Pool interface has no
// place for it: the fnet pool takes it through keyedPool, and the others,
// which have no shards to keep a connection on, go without.
func FnetExecutor(pool Pool) func(key int, task func()) {
	if kp, ok := pool.(keyedPool); ok {
		return func(key int, task func()) {
			if !kp.GoKeyed(key, task) {
				go call(task)
			}
		}
	}
	return func(_ int, task func()) {
		if !pool.Go(task) {
			go call(task)
		}
	}
}

// keyedPool is a Pool that can keep the tasks submitted with one key on one
// of its queues; see FnetExecutor.
type keyedPool interface {
	GoKeyed(key int, f func()) bool
}

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
