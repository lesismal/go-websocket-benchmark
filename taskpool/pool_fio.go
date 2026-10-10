package taskpool

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

	"github.com/antlabs/task/task/driver"

	// stream2 registers itself with fio's task-driver registry.
	_ "github.com/antlabs/task/task"
)

const (
	fioDriverName = "elastic"

	// fio gives each of its event loops a pool of its own, so its own
	// defaults are per-loop counts; this is one pool for the whole process,
	// and these are the process-wide counts the fio servers here have
	// been benchmarked with.
	fioMinWorkers = 256
	fioMaxWorkers = 4096
)

func init() {
	Register(Fio, func(config Config) (Pool, error) {
		minWorkers := orDefault(config.MinWorkers, fioMinWorkers)
		maxWorkers := max(orDefault(config.MaxWorkers, fioMaxWorkers), minWorkers)
		return newFioPool(minWorkers, maxWorkers), nil
	})
}

// fioPool is fio's stream2 pool, which grows its worker count towards
// the ceiling while the machine has CPU left and retires workers that stay
// idle. A submission that finds its queue full waits rather than being
// refused.
//
// stream2 schedules through per-connection executors, each of which runs one
// batch at a time so that a connection's callbacks stay ordered. A general
// pool has no connections to key on, so this spreads submissions over a fixed
// set of executors: tasks that land on the same one are serialised, which is
// why there are as many of them as the pool may run workers.
type fioPool struct {
	tasker driver.Tasker
	shards []*fioShard
	next   atomic.Uint64
	cancel context.CancelFunc
}

type fioShard struct {
	mu       sync.Mutex
	executor driver.TaskExecutor
}

func newFioPool(minWorkers, maxWorkers int) *fioPool {
	ctx, cancel := context.WithCancel(context.Background())
	conf := &driver.Conf{
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	tasker := driver.GetRegister(fioDriverName).New(ctx, minWorkers, minWorkers, maxWorkers, conf)
	pool := &fioPool{tasker: tasker, cancel: cancel, shards: make([]*fioShard, maxWorkers)}
	for index := range pool.shards {
		pool.shards[index] = &fioShard{executor: tasker.NewExecutor()}
	}
	return pool
}

func (p *fioPool) Go(f func()) bool {
	shard := p.shards[(p.next.Add(1)-1)%uint64(len(p.shards))]
	return shard.executor.AddTask(&shard.mu, func() bool { f(); return false }) == nil
}

func (p *fioPool) Workers() int  { return p.tasker.GetGoroutines() }
func (p *fioPool) Rejects() bool { return false }

func (p *fioPool) Stop() {
	p.cancel()
	for _, shard := range p.shards {
		_ = shard.executor.Close(&shard.mu)
	}
}
