package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func collect(t *testing.T, q Queue, want int) [][]byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	var got [][]byte
	done := make(chan struct{})
	go q.Consume(ctx, func(jobs [][]byte) []error {
		mu.Lock()
		got = append(got, jobs...)
		if len(got) >= want {
			select {
			case <-done:
			default:
				close(done)
			}
		}
		mu.Unlock()
		return nil
	})
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("timed out; got %d of %d jobs", len(got), want)
	}
	mu.Lock()
	defer mu.Unlock()
	return got
}

func TestMemQueue(t *testing.T) {
	q := NewMemQueue(2)
	ctx := context.Background()
	q.Enqueue(ctx, []byte("a"))
	q.Enqueue(ctx, []byte("b"))
	if err := q.Enqueue(ctx, []byte("c")); err != ErrFull {
		t.Fatalf("expected ErrFull, got %v", err)
	}
	if got := collect(t, q, 2); string(got[0])+string(got[1]) != "ab" {
		t.Fatalf("got %q", got)
	}
}

func TestRedisQueueAndBroker(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()
	rdb, err := Connect(ctx, "redis://"+mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	q, err := NewRedisQueue(ctx, rdb, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range []string{"one", "two", "three"} {
		if err := q.Enqueue(ctx, []byte(j)); err != nil {
			t.Fatal(err)
		}
	}
	got := collect(t, q, 3)
	if len(got) != 3 || string(got[2]) != "three" {
		t.Fatalf("got %q", got)
	}

	b := NewRedisBroker(ctx, rdb)
	defer b.Close()
	ch, cancel := b.Subscribe(7)
	defer cancel()
	time.Sleep(50 * time.Millisecond) // let the subscription register
	b.Publish(8, []byte("other project"))
	b.Publish(7, []byte("hello"))
	select {
	case m := <-ch:
		if string(m) != "hello" {
			t.Fatalf("got %q", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message")
	}
}

// flaky fails the named jobs a fixed number of times, then succeeds, and
// records every job that was finally persisted.
type flaky struct {
	mu        sync.Mutex
	failsLeft map[string]int
	persisted []string
	calls     map[string]int
}

func (f *flaky) handle(jobs [][]byte) []error {
	f.mu.Lock()
	defer f.mu.Unlock()
	errs := make([]error, len(jobs))
	for i, j := range jobs {
		f.calls[string(j)]++
		if f.failsLeft[string(j)] > 0 {
			f.failsLeft[string(j)]--
			errs[i] = errors.New("database is locked")
			continue
		}
		f.persisted = append(f.persisted, string(j))
	}
	return errs
}

func (f *flaky) wait(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := len(f.persisted)
		f.mu.Unlock()
		if n >= want {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.persisted...)
}

func TestMemQueueRetriesFailedJobs(t *testing.T) {
	q := NewMemQueue(10)
	q.RetryDelay = 5 * time.Millisecond
	var dropped []string
	q.OnDrop = func(job []byte, err error) { dropped = append(dropped, string(job)) }
	f := &flaky{failsLeft: map[string]int{"b": 2, "poison": 1 << 30}, calls: map[string]int{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, j := range []string{"a", "b", "poison"} {
		q.Enqueue(ctx, []byte(j))
	}
	go q.Consume(ctx, f.handle)
	got := f.wait(t, 2)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("persisted %q; a failed write must be retried, not lost", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for q.Pending() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls["poison"] != MaxAttempts || len(dropped) != 1 {
		t.Fatalf("poison job: %d attempts, dropped %v; want %d attempts then dropped", f.calls["poison"], dropped, MaxAttempts)
	}
}

func TestRedisQueueRedeliversFailedJobs(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rdb, err := Connect(ctx, "redis://"+mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	q, err := NewRedisQueue(ctx, rdb, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	q.ClaimIdle = 40 * time.Millisecond
	f := &flaky{failsLeft: map[string]int{"two": 1}, calls: map[string]int{}}
	for _, j := range []string{"one", "two"} {
		q.Enqueue(ctx, []byte(j))
	}
	go q.Consume(ctx, f.handle)
	got := f.wait(t, 2)
	if len(got) != 2 {
		t.Fatalf("persisted %q; the failed job must stay pending and be redelivered", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for q.Pending() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := q.Pending(); n != 0 {
		t.Fatalf("%d jobs left in the stream after success", n)
	}
}
