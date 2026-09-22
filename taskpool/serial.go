package taskpool

import "sync"

// Serial feeds one stream of work to a pool and keeps it in order.
//
// A pool runs whatever it is given on whichever worker is free, so a
// framework that submits one task per message would let a connection's
// replies overtake each other. Serial keeps at most one batch of a stream in
// flight and lets that batch pick up whatever arrived while it ran, which
// costs one submission per burst rather than one per message.
//
// It is the same arrangement as the executor RegisterGreatwsTaskDriver hands
// greatws, which is kept separate because greatws supplies both the lock and
// the task signature and neither should cost an allocation per message.
type Serial struct {
	pool    Pool
	mu      sync.Mutex
	pending []func()
	running bool
}

// NewSerial returns a stream that runs its tasks on pool, in order.
func NewSerial(pool Pool) *Serial { return &Serial{pool: pool} }

// Go queues f behind whatever this stream has not run yet and reports
// whether the pool took the work. A false leaves f queued for the next Go to
// carry rather than dropping it, so the caller can retry or run it itself.
func (s *Serial) Go(f func()) bool {
	s.mu.Lock()
	s.pending = append(s.pending, f)
	if s.running {
		s.mu.Unlock()
		return true
	}
	s.running = true
	s.mu.Unlock()

	if s.pool.Go(s.drain) {
		return true
	}
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	return false
}

// Take removes and returns everything the stream has not run, for a caller
// that has to make good on a Go the pool refused.
func (s *Serial) Take() []func() {
	s.mu.Lock()
	pending := s.pending
	s.pending = nil
	s.mu.Unlock()
	return pending
}

func (s *Serial) drain() {
	// The emptied batch becomes the next one's buffer, so a stream that
	// keeps going allocates a queue once rather than once per batch. It
	// stays a local: a field would be written here, outside the lock, and
	// read under it.
	var spare []func()
	for {
		s.mu.Lock()
		if len(s.pending) == 0 {
			s.running = false
			s.mu.Unlock()
			return
		}
		batch := s.pending
		s.pending = spare[:0]
		s.mu.Unlock()

		for i, task := range batch {
			batch[i] = nil
			call(task)
		}
		spare = batch[:0]
	}
}
