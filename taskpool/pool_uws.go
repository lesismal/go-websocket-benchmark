package taskpool

import (
	"runtime"
	"sync/atomic"
)

// The sizing uws has run with in this benchmark since its executor was added.
const (
	uwsWorkers = 256
	uwsPending = 65536
)

func init() {
	Register(Uws, func(config Config) (Pool, error) {
		return newUwsPool(
			orDefault(config.MaxWorkers, uwsWorkers),
			orDefault(config.QueueSize, uwsPending),
		), nil
	})
}

// uwsPool is the sharded channel executor uws runs on in this benchmark,
// moved here so the other frameworks can be measured on it too.
//
// Its workers are started up front and spread over one channel per shard, so
// that submissions from different pollers rarely contend for the same queue.
// Alone among the pools here it refuses work rather than waiting: a shard
// whose channel is full reports false, which is how uws surfaces application
// backpressure instead of letting a slow handler back up into the poller.
type uwsPool struct {
	shards  []chan func()
	next    atomic.Uint64
	workers int
}

func newUwsPool(workers, pending int) *uwsPool {
	shardCount := min(runtime.GOMAXPROCS(0), max(workers/8, 1), pending)
	pool := &uwsPool{shards: make([]chan func(), shardCount), workers: workers}
	for index := range pool.shards {
		queue := make(chan func(), share(pending, shardCount, index))
		pool.shards[index] = queue
		for range share(workers, shardCount, index) {
			go func() {
				for task := range queue {
					call(task)
				}
			}()
		}
	}
	return pool
}

// share splits total over shards and hands shard index its part, giving the
// remainder to the lowest-numbered shards.
func share(total, shards, index int) int {
	part := total / shards
	if index < total%shards {
		part++
	}
	return part
}

func (p *uwsPool) Go(f func()) bool {
	index := (p.next.Add(1) - 1) % uint64(len(p.shards))
	select {
	case p.shards[index] <- f:
		return true
	default:
		return false
	}
}

func (p *uwsPool) Workers() int  { return p.workers }
func (p *uwsPool) Rejects() bool { return true }

func (p *uwsPool) Stop() {
	for _, queue := range p.shards {
		close(queue)
	}
}
