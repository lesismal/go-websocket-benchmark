package taskpool

import "sync/atomic"

func init() {
	Register(Inline, func(Config) (Pool, error) { return inlinePool{}, nil })
	Register(Goroutine, func(Config) (Pool, error) { return &goPool{}, nil })
}

// inlinePool runs the task on the goroutine that submitted it, which for
// every framework here is an I/O goroutine. It is the baseline the pools are
// measured against: no handoff and no queue, at the cost of a handler that
// blocks stalling every connection its poller holds. greatws calls the same
// arrangement its "io" task mode, and it is what greatws_event runs by
// default.
type inlinePool struct{}

func (inlinePool) Go(f func()) bool { call(f); return true }
func (inlinePool) Workers() int     { return 0 }
func (inlinePool) Rejects() bool    { return false }
func (inlinePool) Stop()            {}

// goPool starts a goroutine per task and bounds nothing, which is what a
// server does when it has no pool at all. It is the other baseline: the
// scheduler absorbs whatever arrives, and a burst costs a goroutine per
// message rather than a queue slot.
type goPool struct{ running atomic.Int64 }

func (p *goPool) Go(f func()) bool {
	p.running.Add(1)
	go func() {
		defer p.running.Add(-1)
		call(f)
	}()
	return true
}

func (p *goPool) Workers() int  { return int(p.running.Load()) }
func (p *goPool) Rejects() bool { return false }
func (p *goPool) Stop()         {}
