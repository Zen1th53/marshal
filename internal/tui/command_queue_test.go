package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// queueTestWorkspace is a workspace with the interactive command lane: its
// results come back through uiEvents, which pump applies on the test's loop.
func queueTestWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w := NewWorkspace(nil, "test", "test")
	w.workDir = t.TempDir()
	w.uiEvents = make(chan func(), 64)
	t.Cleanup(func() {
		w.clearCommandQueue()
		w.cancelCommand()
		deadline := time.Now().Add(5 * time.Second)
		for w.commandBusy.Load() {
			if time.Now().After(deadline) {
				t.Error("command lane did not drain at cleanup")
				return
			}
			select {
			case fn := <-w.uiEvents:
				fn()
			case <-time.After(10 * time.Millisecond):
			}
		}
		w.commandWG.Wait()
	})
	return w
}

// pumpUntil applies UI events until done reports true.
func pumpUntil(t *testing.T, w *Workspace, what string, done func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	// A cancelled command can free the lane off the loop, posting nothing,
	// so done is polled as well as rechecked after each event.
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !done() {
		select {
		case fn := <-w.uiEvents:
			fn()
		case <-tick.C:
		case <-deadline:
			t.Fatalf("timed out waiting for %s; activity: %q", what, w.GetUIState().LastOutput)
		}
	}
}

// recorder logs which commands ran, in order, and whether two ever overlapped.
type recorder struct {
	mu      sync.Mutex
	ran     []string
	running int
	overlap bool
}

func (r *recorder) do(name string, release <-chan struct{}) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		r.mu.Lock()
		r.running++
		if r.running > 1 {
			r.overlap = true
		}
		r.ran = append(r.ran, name)
		r.mu.Unlock()
		if release != nil {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		r.mu.Lock()
		r.running--
		r.mu.Unlock()
		return name + " done", ctx.Err()
	}
}

func (r *recorder) order() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ran...)
}

// A command issued while another runs used to be dropped with only
// "A command is still running". It must run after the first, in order, and
// never alongside it.
func TestCommandWhileBusyRunsAfterIt(t *testing.T) {
	w := queueTestWorkspace(t)
	r := &recorder{}
	release := make(chan struct{})
	ctx := context.Background()
	w.runCommandFunc(ctx, "/marshal use-plan", r.do("use-plan", release))
	w.runCommandFunc(ctx, "/marshal approve-task A-1", r.do("approve-task", nil))
	w.runCommandFunc(ctx, "/marshal status", r.do("status", nil))
	if out := w.GetUIState().LastOutput; !strings.Contains(out, "Queued /marshal approve-task A-1") {
		t.Fatalf("queued command not acknowledged: %q", out)
	}
	if got := r.order(); len(got) > 1 {
		t.Fatalf("a queued command ran while the first still ran: %v", got)
	}
	close(release)
	pumpUntil(t, w, "the queue to drain", func() bool { return !w.commandBusy.Load() })
	w.commandWG.Wait()
	got := r.order()
	if strings.Join(got, ",") != "use-plan,approve-task,status" {
		t.Fatalf("commands ran as %v, want use-plan, approve-task, status in order", got)
	}
	if r.overlap {
		t.Fatal("two commands ran at once")
	}
	if out := w.GetUIState().LastOutput; !strings.Contains(out, "status done") {
		t.Fatalf("last queued result not recorded: %q", out)
	}
}

// The queue is bounded: beyond commandQueueLimit a command is refused with a
// clear message and never runs.
func TestCommandQueueIsBounded(t *testing.T) {
	w := queueTestWorkspace(t)
	r := &recorder{}
	release := make(chan struct{})
	ctx := context.Background()
	w.runCommandFunc(ctx, "first", r.do("first", release))
	for i := range commandQueueLimit {
		name := fmt.Sprintf("queued-%d", i)
		w.runCommandFunc(ctx, name, r.do(name, nil))
	}
	w.runCommandFunc(ctx, "overflow", r.do("overflow", nil))
	if out := w.GetUIState().LastOutput; !strings.Contains(out, "overflow was not run") || !strings.Contains(out, "Ctrl+C") {
		t.Fatalf("overflow not refused clearly: %q", out)
	}
	close(release)
	pumpUntil(t, w, "the queue to drain", func() bool { return !w.commandBusy.Load() })
	w.commandWG.Wait()
	got := r.order()
	if len(got) != commandQueueLimit+1 || strings.Contains(strings.Join(got, ","), "overflow") {
		t.Fatalf("ran %v; want first and %d queued, never overflow", got, commandQueueLimit)
	}
}

// Ctrl+C cancels the running command and clears the queue: nothing queued
// runs afterwards and the lane frees.
func TestCtrlCClearsCommandQueue(t *testing.T) {
	w := queueTestWorkspace(t)
	r := &recorder{}
	ctx := context.Background()
	w.runCommandFunc(ctx, "long", r.do("long", make(chan struct{})))
	w.runCommandFunc(ctx, "queued-1", r.do("queued-1", nil))
	w.runCommandFunc(ctx, "queued-2", r.do("queued-2", nil))
	pumpUntil(t, w, "the first command to start", func() bool { return len(r.order()) == 1 })
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyCtrlC})
	if out := w.GetUIState().LastOutput; !strings.Contains(out, "2 queued commands were dropped") {
		t.Fatalf("Ctrl+C did not say it cleared the queue: %q", out)
	}
	pumpUntil(t, w, "the lane to free", func() bool { return !w.commandBusy.Load() })
	w.commandWG.Wait()
	if got := r.order(); strings.Join(got, ",") != "long" {
		t.Fatalf("queued commands ran after Ctrl+C: %v", got)
	}
	// The lane works again afterwards.
	w.runCommandFunc(ctx, "after", r.do("after", nil))
	pumpUntil(t, w, "the next command", func() bool { return !w.commandBusy.Load() })
	w.commandWG.Wait()
	if got := r.order(); strings.Join(got, ",") != "long,after" {
		t.Fatalf("lane did not accept a command after Ctrl+C: %v", got)
	}
}

// A command whose own context ends (its deadline, or a cancel that is not
// the owner's Ctrl+C) must not strand or drop the commands queued behind it,
// whichever way the result hand-off races the cancellation.
func TestCancelledCommandStillRunsQueue(t *testing.T) {
	w := queueTestWorkspace(t)
	r := &recorder{}
	ctx := context.Background()
	w.runCommandFunc(ctx, "long", r.do("long", make(chan struct{})))
	w.runCommandFunc(ctx, "next", r.do("next", nil))
	pumpUntil(t, w, "the first command to start", func() bool { return len(r.order()) == 1 })
	w.cancelCommand()
	pumpUntil(t, w, "the queued command to run", func() bool { return !w.commandBusy.Load() && len(r.order()) == 2 })
	w.commandWG.Wait()
	if got := r.order(); strings.Join(got, ",") != "long,next" {
		t.Fatalf("queued command after a cancelled one: %v", got)
	}
}

// The same command queued twice in a row (Enter pressed again) waits once.
func TestRepeatedQueuedCommandWaitsOnce(t *testing.T) {
	w := queueTestWorkspace(t)
	r := &recorder{}
	release := make(chan struct{})
	ctx := context.Background()
	w.runCommandFunc(ctx, "first", r.do("first", release))
	w.runCommandFunc(ctx, "retry T1", r.do("retry", nil))
	w.runCommandFunc(ctx, "retry T1", r.do("retry", nil))
	if out := w.GetUIState().LastOutput; !strings.Contains(out, "retry T1 is already queued") {
		t.Fatalf("repeat not reported: %q", out)
	}
	close(release)
	pumpUntil(t, w, "the queue to drain", func() bool { return !w.commandBusy.Load() })
	w.commandWG.Wait()
	if got := r.order(); strings.Join(got, ",") != "first,retry" {
		t.Fatalf("ran %v", got)
	}
}
