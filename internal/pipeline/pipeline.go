// Package pipeline decouples receiving telemetry from writing it: an ingest
// queue (in-process or Redis Streams) and a broker that fans out live events
// to dashboard viewers (in-process or Redis pub/sub).
package pipeline

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrFull means the queue is at capacity; the SDK should retry later.
var ErrFull = errors.New("ingest queue is full")

// Queue buffers ingest jobs between the HTTP handler and the database writer.
type Queue interface {
	Enqueue(ctx context.Context, job []byte) error
	// Consume calls handle with batches of jobs until ctx ends. A batch is
	// acknowledged (removed) only after handle returns.
	Consume(ctx context.Context, handle func(jobs [][]byte))
	// Pending is the number of jobs not yet handled.
	Pending() int64
	Close() error
}

// MemQueue is a bounded in-process queue.
type MemQueue struct {
	ch      chan []byte
	pending atomic.Int64
}

func NewMemQueue(capacity int) *MemQueue {
	return &MemQueue{ch: make(chan []byte, capacity)}
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

func (q *MemQueue) Consume(ctx context.Context, handle func([][]byte)) {
	for {
		var batch [][]byte
		select {
		case <-ctx.Done():
			return
		case j := <-q.ch:
			batch = append(batch, j)
		}
	drain:
		for len(batch) < 64 {
			select {
			case j := <-q.ch:
				batch = append(batch, j)
			default:
				break drain
			}
		}
		handle(batch)
		q.pending.Add(-int64(len(batch)))
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
