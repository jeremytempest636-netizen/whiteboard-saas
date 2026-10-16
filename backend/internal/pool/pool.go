// Package pool provides a small bounded goroutine pool.
//
// It exists so that slow, bursty work (database snapshots, Redis state
// writes) runs on a fixed number of goroutines with a fixed queue instead of
// spawning one goroutine per event. Submission never blocks: when the queue is
// full the caller learns about it immediately and can retry later.
package pool

import (
	"context"
	"sync"
)

// Job is a unit of work.
type Job func()

// Pool runs jobs on a fixed set of worker goroutines.
type Pool struct {
	jobs    chan Job
	wg      sync.WaitGroup
	mu      sync.RWMutex
	closed  bool
	onPanic func(recovered any)
}

// New starts workers goroutines consuming a queue of the given size.
// onPanic (optional) is called when a job panics; the worker keeps running.
func New(workers, queue int, onPanic func(recovered any)) *Pool {
	if workers < 1 {
		workers = 1
	}
	if queue < 1 {
		queue = 1
	}
	p := &Pool{jobs: make(chan Job, queue), onPanic: onPanic}
	p.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go p.worker()
	}
	return p
}

func (p *Pool) worker() {
	defer p.wg.Done()
	for job := range p.jobs {
		p.run(job)
	}
}

func (p *Pool) run(job Job) {
	defer func() {
		if r := recover(); r != nil && p.onPanic != nil {
			p.onPanic(r)
		}
	}()
	job()
}

// Submit enqueues a job without blocking. It returns false when the queue is
// full or the pool is shut down.
func (p *Pool) Submit(job Job) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return false
	}
	select {
	case p.jobs <- job:
		return true
	default:
		return false
	}
}

// Pending returns the number of queued jobs.
func (p *Pool) Pending() int { return len(p.jobs) }

// Shutdown stops accepting work and waits for queued jobs to finish or for ctx
// to expire, whichever comes first.
func (p *Pool) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.jobs)
	}
	p.mu.Unlock()

	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
