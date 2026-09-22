package taskpool

import (
	"github.com/linfeip/fnet"
)

func init() {
	Register(Fnet, func(config Config) (Pool, error) {
		return &fnetPool{pool: fnet.NewWorkerPool(fnet.WorkerPoolConfig{
			// fnet picks the shard count itself from GOMAXPROCS and sizes
			// the rest per shard, so -tpmax and -tpqueue are read per shard
			// here rather than as totals. Leaving them at 0 keeps fnet's own
			// 256 workers and 2048 queued tasks per shard.
			MaxWorkersPerShard: config.MaxWorkers,
			QueueSizePerShard:  config.QueueSize,
		})}, nil
	})
}

// fnetPool is github.com/linfeip/fnet's WorkerPool. Its workers are spawned on
// demand and retired after an idle timeout, so an idle connection holds none,
// and a submission that finds every shard full starts a goroutine of its own
// rather than blocking the reactor or refusing: Go therefore always reports
// true. So does a submission after Stop, which fnet now runs on a goroutine of
// its own instead of dropping, so a drain handed over during shutdown still
// runs and the connection it belongs to is not left with its queue stuck
// behind a task that never ran.
//
// The shared pool is submitted to with fnet's Submit, which picks the less
// loaded of two shards and then, if that shard is saturated, hands the task to
// a shard with an idle worker before queueing it; idle workers steal from busy
// shards from the other side. fnet gives its own pool the connection's id
// instead, which keeps one connection's drains on one shard and so on one
// core's caches; that affinity is not something the Pool interface can carry,
// and it costs correctness nothing because fnet runs one drain per connection
// at a time either way.
type fnetPool struct{ pool *fnet.WorkerPool }

func (p *fnetPool) Go(f func()) bool { p.pool.Submit(f); return true }
func (p *fnetPool) Workers() int     { return p.pool.RunningWorkers() }
func (p *fnetPool) Rejects() bool    { return false }
func (p *fnetPool) Stop()            { p.pool.Close() }
