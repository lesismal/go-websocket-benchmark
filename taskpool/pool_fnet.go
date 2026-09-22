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
// true.
//
// The shared pool is submitted to round-robin. fnet gives its own pool the
// connection's id instead, which keeps one connection's drains on one shard
// and so on one core's caches; that affinity is not something the Pool
// interface can carry, and it costs correctness nothing because fnet runs one
// drain per connection at a time either way.
type fnetPool struct{ pool *fnet.WorkerPool }

func (p *fnetPool) Go(f func()) bool { p.pool.Submit(f); return true }
func (p *fnetPool) Workers() int     { return p.pool.RunningWorkers() }
func (p *fnetPool) Rejects() bool    { return false }
func (p *fnetPool) Stop()            { p.pool.Close() }
