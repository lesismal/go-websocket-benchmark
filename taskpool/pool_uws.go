package taskpool

import (
	"context"
	"runtime"
	"time"

	"github.com/limpo1989/taskgo"
)

// UIO's own sizing: a ceiling of 512 workers per P, which taskgo reaches only
// while tasks block, and idle workers kept long enough to reuse their stacks.
const (
	uwsWorkersPerP = 512
	uwsMaxIdle     = 30 * time.Second
)

func init() {
	Register(Uws, func(config Config) (Pool, error) {
		return newUwsPool(config), nil
	})
}

// uwsPool is github.com/limpo1989/taskgo, the pool UIO runs its connection
// tasks on, set up the way UIO sets it up. taskgo keeps about one worker per P
// running and adds workers, up to MaxWorkers, only while tasks block; an idle
// worker stays parked for 30 seconds so the next task reuses its grown stack.
//
// QueueSize, when set, bounds the tasks submitted but not yet finished
// (taskgo's WithMaxPending), and a submission past it is refused. UIO leaves
// that unbounded, and so does the default here, so the pool refuses nothing
// unless it is asked to. taskgo has no worker floor, so MinWorkers is ignored.
type uwsPool struct {
	queue   *taskgo.Queue
	rejects bool
}

func newUwsPool(config Config) *uwsPool {
	options := []taskgo.Option{
		taskgo.WithConcurrency(orDefault(config.MaxWorkers, uwsWorkersPerP*runtime.GOMAXPROCS(0))),
		taskgo.WithMaxIdle(uwsMaxIdle),
		taskgo.WithPanicHandler(logPanic),
	}
	if config.QueueSize > 0 {
		options = append(options, taskgo.WithMaxPending(config.QueueSize))
	}
	return &uwsPool{queue: taskgo.New(options...), rejects: config.QueueSize > 0}
}

func (p *uwsPool) Go(f func()) bool { return p.queue.Submit(f) }

// Workers is -1: taskgo does not report how many workers it runs.
func (p *uwsPool) Workers() int  { return -1 }
func (p *uwsPool) Rejects() bool { return p.rejects }
func (p *uwsPool) Stop()         { _ = p.queue.Stop(context.Background()) }
