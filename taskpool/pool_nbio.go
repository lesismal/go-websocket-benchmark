package taskpool

import (
	"runtime"

	nbiopool "github.com/lesismal/nbio/taskpool"
)

// nbio sizes the pool its HTTP engine builds at NumCPU*1024 workers over a
// 64k queue; nbhttp.Config calls the first of those MessageHandlerPoolSize.
const (
	nbioWorkersPerCPU = 1024
	nbioQueueSize     = 1024 * 64
)

func init() {
	Register(Nbio, func(config Config) (Pool, error) {
		maxWorkers := orDefault(config.MaxWorkers, runtime.NumCPU()*nbioWorkersPerCPU)
		queueSize := orDefault(config.QueueSize, nbioQueueSize)
		return &nbioPool{pool: nbiopool.New(maxWorkers, queueSize)}, nil
	})
}

// nbioPool is github.com/lesismal/nbio/taskpool. It forks a worker per
// submission while it is under its ceiling and lets each one drain whatever
// is queued before it exits, so the worker count is a ceiling rather than a
// population. A submission over the ceiling waits on the queue rather than
// being refused, and the pool does not expose its worker count.
type nbioPool struct{ pool *nbiopool.TaskPool }

func (p *nbioPool) Go(f func()) bool { p.pool.Go(f); return true }
func (p *nbioPool) Workers() int     { return -1 }
func (p *nbioPool) Rejects() bool    { return false }
func (p *nbioPool) Stop()            { p.pool.Stop() }
