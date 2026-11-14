// Package queue runs the in-process, database-backed job workers.
package queue

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"luduslog/internal/store"
)

type Handler func(ctx context.Context, payload string) error

type Queue struct {
	Store    *store.Store
	Handlers map[string]Handler
	Logger   *slog.Logger
	wg       sync.WaitGroup
	stop     chan struct{}
	once     sync.Once
}

func New(st *store.Store, log *slog.Logger) *Queue {
	return &Queue{Store: st, Handlers: map[string]Handler{}, Logger: log, stop: make(chan struct{})}
}

func (q *Queue) Register(kind string, h Handler) { q.Handlers[kind] = h }

func (q *Queue) Start(workers int) {
	for i := 0; i < workers; i++ {
		q.wg.Add(1)
		go q.worker(i)
	}
}

func (q *Queue) Stop() {
	q.once.Do(func() { close(q.stop) })
	q.wg.Wait()
}

func (q *Queue) worker(n int) {
	defer q.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-q.stop:
			return
		case <-ticker.C:
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				job, err := q.Store.ClaimJob(ctx)
				if err != nil {
					cancel()
					break
				}
				h, ok := q.Handlers[job.Kind]
				if !ok {
					q.Logger.Warn("unknown job kind", "kind", job.Kind, "id", job.ID)
					q.Store.FinishJob(ctx, job.ID)
					cancel()
					continue
				}
				err = h(ctx, job.Payload)
				if err != nil {
					backoff := time.Duration(1<<min(job.Attempts, 8)) * time.Minute
					q.Logger.Warn("job failed", "kind", job.Kind, "id", job.ID, "err", err, "retry_in", backoff)
					if job.Attempts >= 8 {
						q.Store.FinishJob(ctx, job.ID)
					} else {
						q.Store.RetryJob(ctx, job.ID, err.Error(), backoff)
					}
				} else {
					q.Store.FinishJob(ctx, job.ID)
				}
				cancel()
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Enqueue is a convenience wrapper for handlers.
func (q *Queue) Enqueue(ctx context.Context, kind, payload string, delay time.Duration) {
	if err := q.Store.Enqueue(ctx, kind, payload, time.Now().Add(delay)); err != nil {
		q.Logger.Error("enqueue", "kind", kind, "err", err)
	}
}
