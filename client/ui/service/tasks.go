package service

import (
	"context"
	"sync"
)

// taskHub is the in-process half shared by the backup and local task
// services: view subscriptions and the goroutines that follow or execute a
// task. Durable state stays in each service's private index; nothing here is
// persisted or authorizes execution after a restart.
type taskHub[T any] struct {
	mu       sync.Mutex
	watchers map[string]map[int]chan T
	seq      int
	active   map[string]bool
	wg       sync.WaitGroup
	closing  chan struct{}
	closed   bool
	// ctx ends only when Shutdown runs out of time; workers use it for I/O.
	ctx      context.Context
	cancel   context.CancelFunc
	id       func(T) string
	terminal func(T) bool
}

func (h *taskHub[T]) init(id func(T) string, terminal func(T) bool) {
	h.watchers = map[string]map[int]chan T{}
	h.active = map[string]bool{}
	h.closing = make(chan struct{})
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.id, h.terminal = id, terminal
}

func (h *taskHub[T]) isClosing() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

// start runs fn once per task id. It returns false only while closing; a
// task already followed is not started twice.
func (h *taskHub[T]) start(id string, fn func(stop <-chan struct{})) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	if h.active[id] {
		return true
	}
	h.active[id] = true
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer func() { h.mu.Lock(); delete(h.active, id); h.mu.Unlock() }()
		fn(h.closing)
	}()
	return true
}

// notify replaces each subscriber's pending view with v; slow subscribers
// never block the worker. A terminal view closes the subscriptions.
func (h *taskHub[T]) notify(v T) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.id(v)
	for _, ch := range h.watchers[id] {
		select {
		case <-ch:
		default:
		}
		ch <- v
		if h.terminal(v) {
			close(ch)
		}
	}
	if h.terminal(v) {
		delete(h.watchers, id)
	}
}

// watch first delivers current. Unsubscribing never cancels the task.
func (h *taskHub[T]) watch(current T) (<-chan T, func()) {
	ch := make(chan T, 1)
	ch <- current
	if h.terminal(current) {
		close(ch)
		return ch, func() {}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.id(current)
	h.seq++
	n := h.seq
	if h.watchers[id] == nil {
		h.watchers[id] = map[int]chan T{}
	}
	h.watchers[id][n] = ch
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if c, ok := h.watchers[id][n]; ok {
				delete(h.watchers[id], n)
				close(c)
			}
		})
	}
}

// shutdown stops new work, signals workers to reach their safe boundary and
// waits for them. When ctx ends first the workers' I/O is cancelled.
func (h *taskHub[T]) shutdown(ctx context.Context) error {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		close(h.closing)
	}
	h.mu.Unlock()
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// Out of time: interrupt the remaining I/O. Their records are left
		// for the next process to report as interrupted, never rerun.
		h.cancel()
		return ctx.Err()
	}
}
