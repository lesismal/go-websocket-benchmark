package taskpool

import (
	"runtime"

	fnettaskpool "github.com/linfeip/fnet/taskpool"
)

// fnettaskpool.DefaultTaskPool is one shard per P with 4 workers and an
// 8192-task queue each. A shard is capped at fnettaskpool.MaxWorkers.
const (
	fnetWorkersPerShard = 4
	fnetQueuePerShard   = 8192

	// A shard with one worker holds every task queued behind a blocking one,
	// so a ceiling too small to give each shard two gets fewer shards instead.
	fnetMinWorkersPerShard = 2
)

func init() {
	Register(Fnet, func(config Config) (Pool, error) {
		// -tpmax and -tpqueue are totals, as they are for the other pools,
		// and are split equally across the shards. Leaving them at 0 keeps
		// fnet's own sizing.
		shards := runtime.GOMAXPROCS(0)
		workers := fnetWorkersPerShard
		if config.MaxWorkers > 0 {
			shards = max(1, min(shards, config.MaxWorkers/fnetMinWorkersPerShard))
			workers = (config.MaxWorkers + shards - 1) / shards
		}
		queue := fnetQueuePerShard
		if config.QueueSize > 0 {
			queue = (config.QueueSize + shards - 1) / shards
		}
		return &fnetPool{pool: fnettaskpool.New(shards, workers, queue)}, nil
	})
}

// fnetPool is github.com/linfeip/fnet/taskpool's Pool: one bounded lock-free
// queue per shard, and workers that a shard starts on demand up to its limit
// and then keeps, parked until the next task, so MinWorkers has no meaning to
// it and Stop has nothing to release. A submission picks a shard at random
// and never blocks or refuses: one that finds its shard's queue full gets a
// temporary goroutine, which drains the queue before it exits. That is why
// Rejects reports false, and why QueueSize bounds the queue rather than the
// work in flight.
//
// The pool does not expose its worker count.
type fnetPool struct{ pool *fnettaskpool.Pool }

func (p *fnetPool) Go(f func()) bool { p.pool.Submit(f); return true }
func (p *fnetPool) Workers() int     { return -1 }
func (p *fnetPool) Rejects() bool    { return false }
func (p *fnetPool) Stop()            {}
