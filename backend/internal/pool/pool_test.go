package pool

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunsAllJobsAndDrainsOnShutdown(t *testing.T) {
	var n atomic.Int64
	p := New(4, 64, nil)
	for i := 0; i < 50; i++ {
		if !p.Submit(func() { n.Add(1) }) {
			t.Fatal("submit rejected")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 50 {
		t.Fatalf("ran %d jobs", n.Load())
	}
	if p.Submit(func() {}) {
		t.Fatal("submit after shutdown must fail")
	}
}

func TestSubmitDoesNotBlockWhenFull(t *testing.T) {
	block := make(chan struct{})
	p := New(1, 1, nil)
	p.Submit(func() { <-block }) // occupies the worker
	time.Sleep(20 * time.Millisecond)
	p.Submit(func() {}) // fills the queue
	if p.Submit(func() {}) {
		t.Fatal("expected rejection on full queue")
	}
	close(block)
	_ = p.Shutdown(context.Background())
}

func TestPanicIsContained(t *testing.T) {
	var recovered atomic.Bool
	var ran atomic.Bool
	p := New(1, 4, func(any) { recovered.Store(true) })
	p.Submit(func() { panic("boom") })
	p.Submit(func() { ran.Store(true) })
	_ = p.Shutdown(context.Background())
	if !recovered.Load() || !ran.Load() {
		t.Fatalf("recovered=%v ran=%v", recovered.Load(), ran.Load())
	}
}
