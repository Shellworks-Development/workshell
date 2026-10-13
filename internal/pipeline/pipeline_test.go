package pipeline

import (
	"context"
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
	go q.Consume(ctx, func(jobs [][]byte) {
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
