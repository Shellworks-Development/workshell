package pipeline

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	streamKey    = "workshell:ingest"
	deadKey      = "workshell:ingest:dead"
	groupName    = "writers"
	liveChannel  = "workshell:live"
	maxStreamLen = 1_000_000
)

// RedisQueue is a durable ingest queue on a Redis Stream with a consumer
// group, so jobs survive restarts and unacknowledged jobs are re-delivered.
type RedisQueue struct {
	rdb      *redis.Client
	consumer string
	log      *slog.Logger
	// failures counts failed attempts per message on this consumer.
	failures map[string]int
	// ClaimIdle is how long a failed or orphaned job waits before redelivery.
	ClaimIdle time.Duration
}

func Connect(ctx context.Context, url string) (*redis.Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse WORKSHELL_REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect to redis: %w", err)
	}
	return rdb, nil
}

func NewRedisQueue(ctx context.Context, rdb *redis.Client, log *slog.Logger) (*RedisQueue, error) {
	err := rdb.XGroupCreateMkStream(ctx, streamKey, groupName, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return nil, err
	}
	host, _ := os.Hostname()
	return &RedisQueue{rdb: rdb, consumer: fmt.Sprintf("%s-%d", host, os.Getpid()), log: log, failures: map[string]int{}, ClaimIdle: time.Minute}, nil
}

func (q *RedisQueue) Enqueue(ctx context.Context, job []byte) error {
	n, err := q.rdb.XLen(ctx, streamKey).Result()
	if err != nil {
		return err
	}
	if n >= maxStreamLen {
		return ErrFull
	}
	return q.rdb.XAdd(ctx, &redis.XAddArgs{Stream: streamKey, Values: map[string]any{"j": job}}).Err()
}

func (q *RedisQueue) Consume(ctx context.Context, handle Handler) {
	// First re-process anything this consumer read but never acknowledged.
	start := "0"
	lastClaim := time.Time{}
	for ctx.Err() == nil {
		if time.Since(lastClaim) > q.ClaimIdle/2 {
			lastClaim = time.Now()
			// Redeliver jobs that failed here, or that a consumer which died
			// mid-batch never acknowledged.
			msgs, _, err := q.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream: streamKey, Group: groupName, Consumer: q.consumer, MinIdle: q.ClaimIdle, Start: "0", Count: 64,
			}).Result()
			if err == nil && len(msgs) > 0 {
				q.handle(ctx, msgs, handle)
			}
		}
		res, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: groupName, Consumer: q.consumer, Streams: []string{streamKey, start}, Count: 64, Block: 2 * time.Second,
		}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			q.log.Warn("redis read failed", "err", err)
			time.Sleep(time.Second)
			continue
		}
		if len(res) == 0 || len(res[0].Messages) == 0 {
			if start == "0" {
				start = ">" // backlog drained; now read new jobs
			}
			continue
		}
		q.handle(ctx, res[0].Messages, handle)
	}
}

func (q *RedisQueue) handle(ctx context.Context, msgs []redis.XMessage, handle Handler) {
	jobs := make([][]byte, 0, len(msgs))
	jobIDs := make([]string, 0, len(msgs))
	var done []string // acknowledged: persisted, unreadable, or dead-lettered
	for _, m := range msgs {
		if s, ok := m.Values["j"].(string); ok {
			jobs = append(jobs, []byte(s))
			jobIDs = append(jobIDs, m.ID)
		} else {
			done = append(done, m.ID)
		}
	}
	var errs []error
	if len(jobs) > 0 {
		errs = handle(jobs)
	}
	for i, id := range jobIDs {
		err := failed(errs, i)
		if err == nil {
			delete(q.failures, id)
			done = append(done, id)
			continue
		}
		q.failures[id]++
		if q.failures[id] < MaxAttempts {
			continue // left pending; XAutoClaim redelivers it after ClaimIdle
		}
		// Keep the job for inspection instead of retrying it forever.
		q.log.Error("ingest job failed repeatedly; moved to dead-letter stream", "id", id, "stream", deadKey, "err", err)
		if e := q.rdb.XAdd(ctx, &redis.XAddArgs{Stream: deadKey, Values: map[string]any{"j": string(jobs[i]), "id": id, "err": err.Error()}}).Err(); e != nil {
			continue // couldn't park it; try again later rather than lose it
		}
		delete(q.failures, id)
		done = append(done, id)
	}
	if len(done) > 0 {
		q.rdb.XAck(ctx, streamKey, groupName, done...)
		q.rdb.XDel(ctx, streamKey, done...)
	}
}

func (q *RedisQueue) Pending() int64 {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	n, err := q.rdb.XLen(ctx, streamKey).Result()
	if err != nil {
		return 0
	}
	return n
}

func (q *RedisQueue) Close() error { return nil }

// RedisBroker publishes through Redis so every Workshell instance's live
// viewers see events, and relays received messages to local subscribers.
type RedisBroker struct {
	rdb   *redis.Client
	local *MemBroker
	sub   *redis.PubSub
}

func NewRedisBroker(ctx context.Context, rdb *redis.Client) *RedisBroker {
	b := &RedisBroker{rdb: rdb, local: NewMemBroker(), sub: rdb.Subscribe(ctx, liveChannel)}
	go func() {
		for m := range b.sub.Channel() {
			p := []byte(m.Payload)
			if len(p) < 8 {
				continue
			}
			b.local.Publish(int64(binary.BigEndian.Uint64(p[:8])), p[8:])
		}
	}()
	return b
}

func (b *RedisBroker) Publish(projectID int64, msg []byte) {
	p := make([]byte, 8+len(msg))
	binary.BigEndian.PutUint64(p, uint64(projectID))
	copy(p[8:], msg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b.rdb.Publish(ctx, liveChannel, p)
}

func (b *RedisBroker) Subscribe(projectID int64) (<-chan []byte, func()) {
	return b.local.Subscribe(projectID)
}

func (b *RedisBroker) Close() error { return b.sub.Close() }
