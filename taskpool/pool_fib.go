package taskpool

import (
	fib "github.com/lesismal/fib"
	fibpool "github.com/lesismal/fib/taskpool"
)

func init() {
	Register(FibAdaptive, newFibPool(FibAdaptive, fibpool.ModeAdaptive))
	Register(FibCond, newFibPool(FibCond, fibpool.ModeCond))
	Register(FibElastic, newFibPool(FibElastic, fibpool.ModeElastic))
}

// newFibPool builds the factory for one of fib's three modes, whose pools
// fib labels with name in what it logs.
//
// The sizing comes from fib itself rather than from constants copied here,
// because what a worker count buys differs between the modes: it is a
// population of parked goroutines under ModeCond, a ceiling on forked ones
// under ModeElastic, and a ceiling on parked ones under ModeAdaptive. That is
// also why -tpmax means something different in each of them.
func newFibPool(name string, mode fibpool.Mode) Factory {
	return func(config Config) (Pool, error) {
		sizing := fib.DefaultPoolSizing(mode)
		maxWorkers := orDefault(config.MaxWorkers, sizing.WorkerCount)
		queueSize := orDefault(config.QueueSize, sizing.MaxEvents)
		if mode == fibpool.ModeAdaptive && config.MinWorkers > 0 {
			return &fibPool{pool: fibpool.NewAdaptive(fibpool.AdaptiveConfig{
				Name:       name,
				MinWorkers: min(config.MinWorkers, maxWorkers),
				MaxWorkers: maxWorkers,
				QueueSize:  queueSize,
			})}, nil
		}
		return &fibPool{pool: fibpool.NewWithMode(name, mode, maxWorkers, queueSize)}, nil
	}
}

// fibPool is github.com/lesismal/fib/taskpool. A submission that finds the
// queue full waits for room instead of being refused, so Go only reports
// false once the pool has been stopped.
type fibPool struct{ pool *fibpool.TaskPool }

func (p *fibPool) Go(f func()) bool { return p.pool.Go(f) }
func (p *fibPool) Workers() int     { return p.pool.Workers() }
func (p *fibPool) Rejects() bool    { return false }
func (p *fibPool) Stop()            { p.pool.Stop() }
