// Package pipeline decouples receiving telemetry from writing it: an ingest
// queue (in-process or Redis Streams) and a broker that fans out live events
// to dashboard viewers (in-process or Redis pub/sub).
package pipeline

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrFull means the queue is at capacity; the SDK should retry later.
var ErrFull = errors.New("ingest queue is full")

// Handler persists a batch of jobs. It returns one error per job (or nil when
// every job succeeded); a job is acknowledged only when its error is nil, and
// failed jobs are delivered again until MaxAttempts.
type Handler func(jobs [][]byte) []error

// MaxAttempts bounds redelivery of a job that keeps failing, so one poison
// batch can't block the queue forever.
const MaxAttempts = 10

func failed(errs []error, i int) error {
	if i < len(errs) {
		return errs[i]
	}
	return nil
}

// Queue buffers ingest jobs between the HTTP handler and the database writer.
type Queue interface {
	Enqueue(ctx context.Context, job []byte) error
	// Consume calls handle with batches of jobs until ctx ends. Jobs are
	// removed only once handle reports them persisted.
	Consume(ctx context.Context, handle Handler)
	// Pending is the number of jobs not yet handled.
	Pending() int64
	Close() error
}

// MemQueue is a bounded in-process queue. Failed jobs are retried in memory
// with a short backoff; they are lost only if the process exits.
type MemQueue struct {
	ch      chan []byte
	pending atomic.Int64
	// RetryDelay is the wait before failed jobs are handled again.
	RetryDelay time.Duration
	// OnDrop is called when a job is given up on after MaxAttempts.
	OnDrop func(job []byte, err error)
}

func NewMemQueue(capacity int) *MemQueue {
	return &MemQueue{ch: make(chan []byte, capacity), RetryDelay: time.Second}
}

func (q *MemQueue) Enqueue(ctx context.Context, job []byte) error {
	q.pending.Add(1)
	select {
	case q.ch <- job:
		return nil
	default:
		q.pending.Add(-1)
		return ErrFull
	}
}

func (q *MemQueue) Consume(ctx context.Context, handle Handler) {
	type retry struct {
		job      []byte
		attempts int
	}
	var retries []retry
	for {
		var batch [][]byte
		var attempts []int
		if len(retries) > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(q.RetryDelay):
			}
			for _, r := range retries {
				batch = append(batch, r.job)
				attempts = append(attempts, r.attempts)
			}
			retries = nil
		} else {
			select {
			case <-ctx.Done():
				return
			case j := <-q.ch:
				batch = append(batch, j)
				attempts = append(attempts, 0)
			}
		drain:
			for len(batch) < 64 {
				select {
				case j := <-q.ch:
					batch = append(batch, j)
					attempts = append(attempts, 0)
				default:
					break drain
				}
			}
		}
		errs := handle(batch)
		for i, job := range batch {
			err := failed(errs, i)
			switch {
			case err == nil:
				q.pending.Add(-1)
			case attempts[i]+1 >= MaxAttempts:
				q.pending.Add(-1)
				if q.OnDrop != nil {
					q.OnDrop(job, err)
				}
			default:
				retries = append(retries, retry{job, attempts[i] + 1})
			}
		}
	}
}

func (q *MemQueue) Pending() int64 { return q.pending.Load() }
func (q *MemQueue) Close() error   { return nil }

// Broker publishes live events per project.
type Broker interface {
	Publish(projectID int64, msg []byte)
	// Subscribe returns a channel of messages for a project and a cancel func.
	Subscribe(projectID int64) (<-chan []byte, func())
	Close() error
}

// MemBroker fans messages out to in-process subscribers. Slow subscribers
// drop messages rather than block publishers.
type MemBroker struct {
	mu   sync.RWMutex
	subs map[int64]map[chan []byte]struct{}
}

func NewMemBroker() *MemBroker {
	return &MemBroker{subs: map[int64]map[chan []byte]struct{}{}}
}

func (b *MemBroker) Publish(projectID int64, msg []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[projectID] {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (b *MemBroker) Subscribe(projectID int64) (<-chan []byte, func()) {
	ch := make(chan []byte, 256)
	b.mu.Lock()
	if b.subs[projectID] == nil {
		b.subs[projectID] = map[chan []byte]struct{}{}
	}
	b.subs[projectID][ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs[projectID], ch)
			b.mu.Unlock()
		})
	}
}

func (b *MemBroker) Close() error { return nil }
