// Package taskpool collects the goroutine pools the benchmarked frameworks
// ship with behind one interface, so that a framework can be measured on a
// pool other than its own and the pools can be compared on equal footing.
//
// Every pool here is the real thing: the fib, nbio and greatws entries import
// those projects' pools rather than reimplementing them, and the uws entry is
// the sharded executor this benchmark has always given uws, moved out of its
// server so the other frameworks can use it too.
//
// A server picks one with the -taskpool flag; see FromFlags. It defaults to
// FibAdaptive rather than to Default, so that a run nobody configured still
// puts every framework that has a pool hook on one pool; -taskpool=default
// asks for the scheduling each framework ships with instead. The pools differ
// in whether a submission can be refused, which matters to the caller: see
// Pool.Go.
//
// # Ordering
//
// One connection's messages have to be handled, and answered, in the order
// they arrived: a run where a connection's echoes overtook each other would
// not be measuring what a websocket server has to do. No pool here promises
// that on its own - each runs whatever it is given on whichever worker is
// free, and "go" starts a goroutine per task - so the order comes from never
// giving a pool more than one task per connection at a time:
//
//   - fib submits the connection itself as the task and holds a scheduled
//     flag while it is in flight, so one connection is never in the pool
//     twice; a round handles everything readable and returns.
//   - nbio keeps a job list per connection and submits a drain only when the
//     list was empty, so the pool sees one drain per connection at a time.
//     Its blocking connections read on a goroutine of their own and never
//     reach the pool at all.
//   - uws keeps a mailbox per connection with a single runner, likewise.
//   - greatws asks each connection for its own executor;
//     RegisterGreatwsTaskDriver gives it one that keeps at most one drain in
//     flight, which is where its part of this lives.
//   - fnet has no such arrangement to lend, since it calls OnMessage inline
//     on its reactor, so its server wraps the pool in a Serial per
//     connection.
//
// The first four are the frameworks' own invariants, which is why nothing is
// layered on top of them: a second queue would cost a handoff per burst and
// measure a scheduling step the framework does not take. Serial and the
// greatws executor are the two this package owns, and TestSerialKeepsOrder
// and TestGreatwsTaskDriverKeepsAConnectionsOrder hold both to it on every
// registered pool.
//
// Refusal keeps the order too: a pool that declines leaves the queue intact
// for the next submission to carry, or the caller runs it inline, and neither
// lets a later message past an earlier one. See Pool.Go.
package taskpool

import (
	"flag"
	"fmt"
	"runtime"
	"sort"
	"sync"

	"go-websocket-benchmark/logging"
)

// The implementations, as -taskpool takes them.
const (
	// Default leaves a framework running its own built-in scheduling. New
	// returns a nil Pool for it. It is a choice the flag has to be given,
	// not the flag's own default: see FromFlags.
	Default = "default"

	// Inline and Goroutine are the two baselines the pools sit between: no
	// pool at all, and an unbounded one.
	Inline    = "inline"
	Goroutine = "go"

	// The pools the frameworks themselves ship with. FibAdaptive is the
	// mode fib runs by default.
	FibAdaptive = "fib_adaptive"
	FibCond     = "fib_cond"
	FibElastic  = "fib_elastic"
	Nbio        = "nbio"
	Greatws     = "greatws"
	Uws         = "uws"
)

// Pool runs tasks on goroutines it owns.
type Pool interface {
	// Go schedules f and reports whether the pool took it. A pool that
	// reports false has not run f and never will, so the caller has to
	// decide between dropping the work and running it itself. Pools that
	// queue or block instead of refusing always report true; Rejects says
	// which ones those are.
	Go(f func()) bool

	// Workers reports how many goroutines the pool is running, or -1 when
	// the implementation does not track that.
	Workers() int

	// Rejects reports whether Go can ever return false.
	Rejects() bool

	// Stop releases the pool's goroutines. The servers stop their pool only
	// on shutdown, so submitting after Stop is not defined.
	Stop()
}

// Config describes the pool to build. Every size is a request rather than a
// promise: zero leaves the implementation's own default, and an implementation
// that has no use for a field ignores it.
type Config struct {
	// Name selects the implementation; Names lists them.
	Name string

	// MinWorkers is the floor that a pool which grows and shrinks retires
	// to. Pools with a fixed population ignore it.
	MinWorkers int

	// MaxWorkers is how many goroutines the pool may run: a population for
	// the pools that start their workers up front, a ceiling for those that
	// fork on demand.
	MaxWorkers int

	// QueueSize is how many tasks may wait for a worker.
	QueueSize int
}

// A Factory builds one pool from a Config.
type Factory func(Config) (Pool, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds an implementation under name. It panics on a duplicate, the
// way a name collision between two init functions is a bug rather than a
// runtime condition.
func Register(name string, newPool Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if name == Default {
		panic("taskpool: " + Default + " is reserved")
	}
	if _, dup := registry[name]; dup {
		panic("taskpool: Register called twice for " + name)
	}
	registry[name] = newPool
}

// Names lists every implementation, with Default first since it is the one
// that builds no pool rather than one of the pools proper.
func Names() []string {
	registryMu.RLock()
	names := make([]string, 0, len(registry)+1)
	for name := range registry {
		names = append(names, name)
	}
	registryMu.RUnlock()
	sort.Strings(names)
	return append([]string{Default}, names...)
}

// New builds the pool config describes. It returns a nil Pool, and no error,
// for Default and for the empty name: those mean the caller keeps whatever
// scheduling it already had.
func New(config Config) (Pool, error) {
	if config.Name == "" || config.Name == Default {
		return nil, nil
	}
	registryMu.RLock()
	newPool := registry[config.Name]
	registryMu.RUnlock()
	if newPool == nil {
		return nil, fmt.Errorf("taskpool: unknown pool %q, want one of %v", config.Name, Names())
	}
	return newPool(config)
}

var (
	flagName       = flag.String("taskpool", FibAdaptive, "goroutine pool the server runs its callbacks on, or \"default\" for the framework's own; see taskpool.Names")
	flagMinWorkers = flag.Int("tpmin", 0, "taskpool: worker floor, 0 for the pool's own default")
	flagMaxWorkers = flag.Int("tpmax", 0, "taskpool: worker ceiling, 0 for the pool's own default")
	flagQueueSize  = flag.Int("tpqueue", 0, "taskpool: queued tasks, 0 for the pool's own default")
)

// FlagConfig reports the Config the command line describes. Call it after
// flag.Parse.
func FlagConfig() Config {
	return Config{
		Name:       *flagName,
		MinWorkers: *flagMinWorkers,
		MaxWorkers: *flagMaxWorkers,
		QueueSize:  *flagQueueSize,
	}
}

// FromFlags builds the pool the command line selects and logs which one it
// is, so that a report can be read back against the pool that produced it. It
// returns nil only for -taskpool=default, which leaves the server on its own
// scheduling, and exits on a name that names no pool, since a run that
// silently ignored the flag would be reported under the wrong one.
func FromFlags() Pool { return fromFlags(Default) }

// FromFlagsDefault is FromFlags for a server whose own scheduling already is
// one of the pools here, and so has nothing to fall back to: it builds
// fallback for -taskpool=default and never returns nil.
func FromFlagsDefault(fallback string) Pool { return fromFlags(fallback) }

func fromFlags(fallback string) Pool {
	config := FlagConfig()
	if config.Name == Default {
		config.Name = fallback
	}
	pool, err := New(config)
	if err != nil {
		logging.Fatalf("taskpool.New failed: %v", err)
	}
	if pool == nil {
		logging.Printf("taskpool: %s (the framework's own)", Default)
		return nil
	}
	logging.Printf(
		"taskpool: %s min=%d max=%d queue=%d workers=%d rejects=%v GOMAXPROCS=%d NumCPU=%d",
		config.Name, config.MinWorkers, config.MaxWorkers, config.QueueSize,
		pool.Workers(), pool.Rejects(), runtime.GOMAXPROCS(0), runtime.NumCPU(),
	)
	return pool
}

// orDefault reports requested when it is positive and fallback otherwise, so
// that every implementation reads its sizing the same way.
func orDefault(requested, fallback int) int {
	if requested > 0 {
		return requested
	}
	return fallback
}

// call runs f and swallows a panic, which every pool these wrap does for
// itself; the wrappers that reach a pool through an interface it does not
// protect use this so that one bad task cannot take the server down.
func call(f func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logging.Printf("taskpool: task panicked: %v\n%s", recovered, stack())
		}
	}()
	f()
}

func stack() []byte {
	buf := make([]byte, 64<<10)
	return buf[:runtime.Stack(buf, false)]
}
