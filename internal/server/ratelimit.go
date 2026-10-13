package server

import (
	"sync"
	"time"
)

// bucket is a token bucket: capacity tokens, refilled at rate per second.
type bucket struct {
	tokens float64
	last   time.Time
}

// limiter is a keyed set of token buckets.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

func newLimiter() *limiter { return &limiter{buckets: map[string]*bucket{}} }

// take tries to remove n tokens from key's bucket; it returns false (and
// removes nothing) if there aren't enough.
func (l *limiter) take(key string, n, capacity, perSecond float64) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: capacity, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(capacity, b.tokens+now.Sub(b.last).Seconds()*perSecond)
	b.last = now
	if b.tokens < n {
		return false
	}
	b.tokens -= n
	return true
}

// sweep drops buckets idle long enough to be full again.
func (l *limiter) sweep(idle time.Duration) {
	cutoff := time.Now().Add(-idle)
	l.mu.Lock()
	for k, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
	l.mu.Unlock()
}

type limiters struct {
	ingest *limiter // items per project
	client *limiter // client-originated items per player
	logs   *limiter // log lines per game server
	login  *limiter // login attempts per IP
	flags  *limiter // auto-detections per player (so floods log once)
}

func newLimiters() *limiters {
	return &limiters{ingest: newLimiter(), client: newLimiter(), logs: newLimiter(), login: newLimiter(), flags: newLimiter()}
}

func (l *limiters) sweep() {
	for _, x := range []*limiter{l.ingest, l.client, l.logs, l.login, l.flags} {
		x.sweep(15 * time.Minute)
	}
}
