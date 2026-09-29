package taskpool

import (
	fnetpool "github.com/linfeip/fnet/pool"
)

func init() {
	Register(Fnet, func(config Config) (Pool, error) {
		return &fnetPool{pool: fnetpool.New(fnetpool.Config{
			// fnet splits MaxWorkers equally across its shards (about one
			// per core, fewer when the bound cannot give each 256), so
			// -tpmax is a total here rather than a per-shard figure. Its
			// queue has no bound, so -tpqueue is ignored. Leaving MaxWorkers
			// at 0 keeps fnet's own 1024 workers per GOMAXPROCS, and at
			// least 4096.
			MaxWorkers: config.MaxWorkers,
		})}, nil
	})
}

// fnetPool is github.com/linfeip/fnet/pool's Pool. Its workers are spawned on
// demand and retired after an idle timeout, so an idle connection holds none,
// and a submission that finds every worker busy queues rather than blocking
// the reactor or refusing: Submit fails only after Close, which the Pool
// interface leaves undefined, so Rejects reports false.
//
// The shared pool is submitted to with fnet's Submit, which picks a shard at
// random, takes the next one whose lock is free, and moves a task off a
// saturated shard to one with room; idle workers steal from busy shards from
// the other side. fnet gives its own pool the connection's id instead, which
// keeps one connection's drains on one shard and so on one core's caches;
// that affinity is not something the Pool interface can carry, and it costs
// correctness nothing because fnet runs one drain per connection at a time
// either way.
type fnetPool struct{ pool *fnetpool.Pool }

func (p *fnetPool) Go(f func()) bool { return p.pool.Submit(f) == nil }
func (p *fnetPool) Workers() int     { return p.pool.RunningWorkers() }
func (p *fnetPool) Rejects() bool    { return false }
func (p *fnetPool) Stop()            { p.pool.Close() }
