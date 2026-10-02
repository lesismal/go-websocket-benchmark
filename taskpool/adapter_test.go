package taskpool

import (
	"sync"
	"testing"
	"time"

	"github.com/antlabs/task/task/driver"
	fibpool "github.com/lesismal/fib/taskpool"
	"github.com/urpc/uio"
)

func TestFibTaskPoolReportsTheAcceptedPrefix(t *testing.T) {
	// fib closes the connections behind the tasks the pool did not take, so
	// a short count has to be the length of the prefix that ran.
	var ran sync.WaitGroup
	tasks := make([]fibpool.Task, 3)
	for i := range tasks {
		ran.Add(1)
		tasks[i] = fibpool.Task(taskFuncTest(ran.Done))
	}

	adapter := FibTaskPool{Pool: refusingPool{}}
	if accepted := adapter.GoTasks(tasks); accepted != 0 {
		t.Errorf("GoTasks reported %d accepted by a refusing pool, want 0", accepted)
	}
	if adapter.GoTask(tasks[0]) {
		t.Error("GoTask reported that a refusing pool took the task")
	}

	adapter = FibTaskPool{Pool: newTestPool(t, Goroutine)}
	if accepted := adapter.GoTasks(tasks); accepted != len(tasks) {
		t.Errorf("GoTasks reported %d accepted, want %d", accepted, len(tasks))
	}
	waitOrFail(t, &ran, "fib tasks did not run")
}

type taskFuncTest func()

func (f taskFuncTest) RunTask() { f() }

func TestNbioExecuteRunsWhatThePoolRefuses(t *testing.T) {
	// nbio drives its parser through the executor, so a step that never
	// runs leaves the connection stuck rather than dropping one message.
	ran := false
	NbioExecute(refusingPool{})(func() { ran = true })
	if !ran {
		t.Error("NbioExecute dropped the task its pool refused")
	}
}

// taskFunc is a uio.IOTask that runs a func.
type taskFunc func()

func (f taskFunc) RunTask() { f() }

func TestUwsExecutorPassesTheRefusalThrough(t *testing.T) {
	// UIO closes the connection behind a task its executor refuses, so the
	// adapter must not paper over a refusal.
	refusing := UwsExecutor{Pool: refusingPool{}}
	if refusing.Submit(taskFunc(func() {})) {
		t.Error("Submit reported that a refusing pool took the task")
	}
	if n := refusing.SubmitBatch([]uio.IOTask{taskFunc(func() {}), taskFunc(func() {})}); n != 0 {
		t.Errorf("SubmitBatch reported %d tasks taken by a refusing pool", n)
	}
	var done sync.WaitGroup
	done.Add(3)
	live := UwsExecutor{Pool: newTestPool(t, Goroutine)}
	if !live.Submit(taskFunc(done.Done)) {
		t.Fatal("Submit declined work a live pool should have taken")
	}
	if n := live.SubmitBatch([]uio.IOTask{taskFunc(done.Done), taskFunc(done.Done)}); n != 2 {
		t.Fatalf("SubmitBatch took %d of 2 tasks a live pool should have taken", n)
	}
	waitOrFail(t, &done, "uws task did not run")
}

// submitsWithoutRunningInline hands task to executor and fails the test if
// executor does not return while the task is still blocked, which is what it
// would do if it ran the task on the caller's stack. The task finishes once
// executor has returned.
func submitsWithoutRunningInline(t *testing.T, executor func(func()), task func()) {
	t.Helper()
	release := make(chan struct{})
	submitted := make(chan struct{})
	go func() {
		defer close(submitted)
		executor(func() { <-release; task() })
	}()
	select {
	case <-submitted:
	case <-time.After(30 * time.Second):
		close(release)
		t.Fatal("the executor ran the task on the caller's stack")
	}
	close(release)
}

func TestFnetExecutorRunsWhatThePoolRefuses(t *testing.T) {
	// A dropped task would leave fnet's connection with a task it believes is
	// in flight, and so silent for good; running it on the caller would break
	// fnet's rule that an executor never does.
	var done sync.WaitGroup
	done.Add(1)
	submitsWithoutRunningInline(t, FnetExecutor(refusingPool{}), done.Done)
	waitOrFail(t, &done, "the refused task did not run")
}

func TestFnetExecutorRunsWhatALivePoolTakes(t *testing.T) {
	// The task has to reach the pool, not just be handed to an executor
	// built and thrown away: a task that never runs leaves the connection
	// silent.
	var done sync.WaitGroup
	done.Add(1)
	submitsWithoutRunningInline(t, FnetExecutor(newTestPool(t, Goroutine)), done.Done)
	waitOrFail(t, &done, "fnet task did not run")
}

func TestGreatwsTaskDriverKeepsAConnectionsOrder(t *testing.T) {
	// greatws expects one connection's callbacks to run in the order they
	// were added however many workers the pool has.
	executor := (&greatwsTaskDriver{pool: newTestPool(t, Goroutine)}).NewExecutor()

	var mu sync.Mutex
	const tasks = 500
	order := make([]int, 0, tasks)
	var done sync.WaitGroup
	done.Add(tasks)
	for i := range tasks {
		if err := executor.AddTask(&mu, func() bool {
			order = append(order, i)
			done.Done()
			return false
		}); err != nil {
			t.Fatalf("AddTask failed: %v", err)
		}
	}
	waitOrFail(t, &done, "greatws executor did not finish")

	mu.Lock()
	defer mu.Unlock()
	if len(order) != tasks {
		t.Fatalf("executor ran %d of %d tasks", len(order), tasks)
	}
	for i, got := range order {
		if got != i {
			t.Fatalf("executor ran task %d in position %d", got, i)
		}
	}
	if err := executor.Close(nil); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestGreatwsTaskDriverLeavesRefusedWorkQueued(t *testing.T) {
	// A refusal has to leave the task for the next AddTask to carry:
	// dropping it would lose a message rather than delay one.
	executor := (&greatwsTaskDriver{pool: refusingPool{}}).NewExecutor()

	var mu sync.Mutex
	if err := executor.AddTask(&mu, func() bool { return false }); err == nil {
		t.Fatal("AddTask hid the pool's refusal")
	}
	queue := executor.(*greatwsTaskExecutor)
	mu.Lock()
	pending, running := len(queue.pending), queue.running
	mu.Unlock()
	if pending != 1 {
		t.Errorf("executor holds %d refused tasks, want 1", pending)
	}
	if running {
		t.Error("executor still believes a drain is in flight")
	}
}

func TestGreatwsTaskDriverIsItsOwnTasker(t *testing.T) {
	// greatws asks each driver for a Tasker per event loop; every loop has
	// to land on the one pool the benchmark configured.
	pool := newTestPool(t, Goroutine)
	var taskDriver driver.TaskDriver = &greatwsTaskDriver{pool: pool}
	first := taskDriver.New(t.Context(), 1, 1, 1, &driver.Conf{})
	second := taskDriver.New(t.Context(), 1, 1, 1, &driver.Conf{})
	if first != second {
		t.Error("two event loops were given two taskers, want the one pool")
	}
	if first.GetGoroutines() != pool.Workers() {
		t.Error("GetGoroutines does not report the pool's workers")
	}
}
