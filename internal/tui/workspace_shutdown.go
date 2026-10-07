package tui

import (
	"context"
	"sync"
)

// workerLifecycle serializes admission with shutdown so Add cannot race Wait.
// It also works for workspaces and navigation views constructed by tests.
type workerLifecycle struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
	wg     sync.WaitGroup
	bound  map[context.Context]context.CancelFunc
}

func (l *workerLifecycle) context() context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ctx == nil {
		l.ctx, l.cancel = context.WithCancel(context.Background())
		if l.closed {
			l.cancel()
		}
	}
	return l.ctx
}

// bind preserves caller cancellation while also ending detached work on shutdown.
func (l *workerLifecycle) bind(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancel(ctx)
	l.mu.Lock()
	if l.closed {
		cancel()
	} else {
		if l.bound == nil {
			l.bound = make(map[context.Context]context.CancelFunc)
		}
		l.bound[ctx] = cancel
	}
	l.mu.Unlock()
	context.AfterFunc(ctx, func() {
		l.mu.Lock()
		delete(l.bound, ctx)
		l.mu.Unlock()
	})
	return ctx
}

func (l *workerLifecycle) start(ctx context.Context, group *sync.WaitGroup, fn func(context.Context)) bool {
	ctx, cancel := context.WithCancel(ctx)
	ctx = l.bind(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		cancel()
		return false
	}
	l.wg.Add(1)
	if group != nil {
		group.Add(1)
	}
	go func() {
		defer l.wg.Done()
		if group != nil {
			defer group.Done()
		}
		defer cancel()
		fn(ctx)
	}()
	return true
}

func (l *workerLifecycle) stop() {
	l.mu.Lock()
	l.closed = true
	if l.cancel != nil {
		l.cancel()
	}
	for _, cancel := range l.bound {
		cancel()
	}
	l.mu.Unlock()
}

func (w *Workspace) startBackground(ctx context.Context, fn func(context.Context)) bool {
	return w.workers.start(ctx, nil, fn)
}

// Marshal may hand its context to a native turn that outlives this operation.
// Keep that context until its explicit cancellation or workspace shutdown.
func (w *Workspace) startMarshalBackground(ctx context.Context, fn func(context.Context)) bool {
	ctx = w.workers.bind(ctx)
	return w.startBackground(ctx, func(context.Context) { fn(ctx) })
}

// Close cancels and joins workspace work before the caller closes its runtime
// or store. Native tmux sessions remain open; only their local observers stop.
// Close is safe to call repeatedly. A closed workspace cannot start new work.
func (w *Workspace) Close() {
	w.workers.stop()
	if w.navView != nil {
		w.navView.workers.stop()
	}
	w.cancelCommand()
	w.cancelGovernedDispatches()
	w.workers.wg.Wait()
	if w.navView != nil {
		w.navView.workers.wg.Wait()
		w.navView.Wait()
	}
}
