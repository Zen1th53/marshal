package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// launchHost is a portable process table standing in for the OS: a held
// process is alive but does no work until its barrier is released.
type launchHost struct {
	mu      sync.Mutex
	boot    string
	next    int
	procs   []*launchProc
	findErr error
}

type launchProc struct {
	id      driver.ProcessIdentity
	key     string
	alive   bool
	working bool
}

func (h *launchHost) BootID() (string, error) { h.mu.Lock(); defer h.mu.Unlock(); return h.boot, nil }
func (h *launchHost) Find(key string) ([]driver.ProcessIdentity, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.findErr != nil {
		return nil, h.findErr
	}
	var out []driver.ProcessIdentity
	for _, p := range h.procs {
		if p.alive && p.key == key && p.id.BootID == h.boot {
			out = append(out, p.id)
		}
	}
	return out, nil
}
func (h *launchHost) Alive(id driver.ProcessIdentity) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range h.procs {
		if p.id == id {
			return p.alive && id.BootID == h.boot, nil
		}
	}
	return false, nil
}
func (h *launchHost) Stop(id driver.ProcessIdentity) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range h.procs {
		if p.id == id {
			p.alive = false
		}
	}
	return nil
}
func (h *launchHost) reboot() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.boot += "-next"
	for _, p := range h.procs {
		p.alive = false
	}
}

// crashJournal stops the owner (as a canceled acknowledgement) when it is
// about to persist the named stage.
type crashJournal struct {
	driver.FileLaunchJournal
	crashAt driver.LaunchStage
}

func (j *crashJournal) Store(rec driver.LaunchReceipt) error {
	if j.crashAt != "" && rec.Stage == j.crashAt {
		j.crashAt = ""
		return context.Canceled
	}
	return j.FileLaunchJournal.Store(rec)
}

// heldWorker is a supervised driver whose work runs a real native process
// only after the supervisor opens its barrier.
type heldWorker struct {
	inner   driver.Native
	host    *launchHost
	journal driver.LaunchJournal
	// crashBeforeSpawn models an owner that stopped after its intent.
	crashBeforeSpawn bool
	// ownerDead leaves a held child in place instead of aborting it.
	ownerDead bool
	mu        sync.Mutex
	works     int
	early     int
}

func (w *heldWorker) Mode() marshal.WorkerMode { return marshal.Native }
func (w *heldWorker) Launch(context.Context, driver.Request) (*driver.Handle, error) {
	return nil, errors.New("unsupervised launch")
}
func (w *heldWorker) Wait(ctx context.Context, h *driver.Handle) (marshal.HandIn, error) {
	return w.inner.Wait(ctx, h)
}
func (w *heldWorker) Cancel(h *driver.Handle) error { return w.inner.Cancel(h) }
func (w *heldWorker) LaunchHeld(ctx context.Context, req driver.Request, key string) (*driver.HeldStart, error) {
	if w.crashBeforeSpawn {
		w.crashBeforeSpawn = false
		return nil, context.Canceled
	}
	w.host.mu.Lock()
	w.host.next++
	p := &launchProc{id: driver.ProcessIdentity{BootID: w.host.boot, PID: w.host.next, Start: "start"}, key: key, alive: true}
	w.host.procs = append(w.host.procs, p)
	w.host.mu.Unlock()
	return &driver.HeldStart{Process: p.id,
		Release: func() (*driver.Handle, error) {
			rec, ok, err := w.journal.Load(key)
			w.mu.Lock()
			if err != nil || !ok || rec.Stage != driver.LaunchReceipted || rec.Process != p.id {
				w.early++
			}
			w.works++
			w.mu.Unlock()
			p.working = true
			return w.inner.Launch(ctx, req)
		},
		Abort: func() error {
			if !w.ownerDead {
				_ = w.host.Stop(p.id)
			}
			return nil
		}}, nil
}

func (w *heldWorker) count() (int, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.works, w.early
}

type unsupervisedWorker struct{ inner driver.Native }

func (u unsupervisedWorker) Mode() marshal.WorkerMode { return marshal.Native }
func (u unsupervisedWorker) Launch(ctx context.Context, req driver.Request) (*driver.Handle, error) {
	return u.inner.Launch(ctx, req)
}
func (u unsupervisedWorker) Wait(ctx context.Context, h *driver.Handle) (marshal.HandIn, error) {
	return u.inner.Wait(ctx, h)
}
func (u unsupervisedWorker) Cancel(h *driver.Handle) error { return u.inner.Cancel(h) }

type launchFixture struct {
	s       *MarshalService
	host    *launchHost
	journal *crashJournal
	worker  *heldWorker
}

func newLaunchFixture(t *testing.T) launchFixture {
	t.Helper()
	s, _ := marshalFixture(t, 1)
	draft := s.Model.(marshalFakeModel).draft
	draft.Tasks[0].Mode = marshal.Native
	s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	host := &launchHost{boot: "boot-1"}
	journal := &crashJournal{FileLaunchJournal: driver.FileLaunchJournal{Dir: filepath.Join(t.TempDir(), "launches")}}
	inner := driver.Native{Provider: "fake", Binary: "sh", Args: func(driver.Request) []string { return []string{"-c", "printf done > a.txt"} }, Parse: func([]byte) []marshal.CommandRecord { return nil }}
	w := &heldWorker{inner: inner, host: host, journal: journal}
	s.Drivers["worker"] = w
	s.LaunchSupervisor = &driver.LaunchSupervisor{Journal: journal, Processes: host}
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	return launchFixture{s: s, host: host, journal: journal, worker: w}
}

// crashedKey returns the launch key the interrupted dispatch persisted.
func (f launchFixture) crashedKey(t *testing.T) string {
	t.Helper()
	run, _, err := f.s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Operation == nil || run.Operation.Next == nil || run.Operation.Next.Tasks[0].LaunchKey == "" {
		t.Fatalf("interrupted launch retained no keyed operation: %+v", run.Operation)
	}
	return run.Operation.Next.Tasks[0].LaunchKey
}

// restart models a new owner: fresh supervisor memory over the same durable
// receipts and process table, then startup recovery.
func (f launchFixture) restart(t *testing.T) marshal.Run {
	t.Helper()
	f.s.AfterLifecycleEffect = nil
	f.s.LaunchSupervisor = &driver.LaunchSupervisor{Journal: f.journal, Processes: f.host}
	_ = f.s.RecoverPendingOperations(t.Context())
	run, _, err := f.s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func (f launchFixture) expectRelaunchOnce(t *testing.T, key string) {
	t.Helper()
	run := f.restart(t)
	if run.Operation != nil || run.Tasks[0].State != marshal.Queued || run.Tasks[0].LaunchKey != key {
		t.Fatalf("proven-absent launch not requeued under its key: op=%+v task=%+v", run.Operation, run.Tasks[0])
	}
	d, err := f.s.Dispatch(t.Context(), "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := d.Handle.LaunchReceipt(); !ok || rec.Key != key {
		t.Fatalf("relaunch used another key: %+v", rec)
	}
	if _, err := f.s.CollectHandIn(t.Context(), "run", d); err != nil {
		t.Fatal(err)
	}
	f.expectWorks(t, 1)
	f.expectKeyRetired(t, key)
}

// expectKeyRetired proves an adopted result frees the key, so the next
// attempt mints a fresh one instead of being held behind the old receipt.
func (f launchFixture) expectKeyRetired(t *testing.T, key string) {
	t.Helper()
	run, _, err := f.s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].LaunchKey != "" {
		t.Fatalf("hand-in kept the launch key %q", run.Tasks[0].LaunchKey)
	}
	if disp, err := f.s.LaunchSupervisor.Inspect(key); err != nil || disp != driver.LaunchAbsent {
		t.Fatalf("hand-in kept the receipt: %s %v", disp, err)
	}
}

func (f launchFixture) expectWorks(t *testing.T, want int) {
	t.Helper()
	works, early := f.worker.count()
	if works != want || early != 0 {
		t.Fatalf("worker started %d times (want %d), %d before a durable receipt", works, want, early)
	}
}

func (f launchFixture) expectHeldWorker(t *testing.T, key string, want driver.LaunchDisposition) {
	t.Helper()
	run := f.restart(t)
	if run.Tasks[0].State != marshal.Dispatched {
		t.Fatalf("started worker's reservation released: %+v", run.Tasks[0])
	}
	if disp, err := f.s.LaunchSupervisor.Inspect(key); err != nil || disp != want {
		t.Fatalf("disposition %s %v, want %s", disp, err, want)
	}
	if _, err := f.s.Dispatch(t.Context(), "run", "a", "write"); err == nil {
		t.Fatal("dispatched a task whose worker already started")
	}
	if launched, err := f.s.dispatchReady(t.Context(), "run", run, marshalBrief); len(launched) != 0 {
		t.Fatalf("execution relaunched a started worker: %v %v", launched, err)
	}
	var held *driver.LaunchHeldError
	if _, err := f.s.LaunchSupervisor.Launch(t.Context(), key, f.worker, driver.Request{RunID: "run", Task: run.Tasks[0], Worktree: t.TempDir(), Brief: "write"}, nil); !errors.As(err, &held) || held.Disposition != want {
		t.Fatalf("same key launched again: %v", err)
	}
	f.expectWorks(t, 1)
}

func TestLaunchCrashWindowsNeverDuplicateWorker(t *testing.T) {
	t.Run("before intent", func(t *testing.T) {
		f := newLaunchFixture(t)
		f.journal.crashAt = driver.LaunchIntent
		if _, err := f.s.Dispatch(t.Context(), "run", "a", "write"); !errors.Is(err, context.Canceled) {
			t.Fatalf("crash not observed: %v", err)
		}
		f.expectRelaunchOnce(t, f.crashedKey(t))
	})
	t.Run("after intent before spawn", func(t *testing.T) {
		f := newLaunchFixture(t)
		f.worker.crashBeforeSpawn = true
		if _, err := f.s.Dispatch(t.Context(), "run", "a", "write"); !errors.Is(err, context.Canceled) {
			t.Fatalf("crash not observed: %v", err)
		}
		f.expectRelaunchOnce(t, f.crashedKey(t))
	})
	t.Run("after spawn before receipt", func(t *testing.T) {
		f := newLaunchFixture(t)
		f.journal.crashAt = driver.LaunchReceipted
		f.worker.ownerDead = true
		if _, err := f.s.Dispatch(t.Context(), "run", "a", "write"); !errors.Is(err, context.Canceled) {
			t.Fatalf("crash not observed: %v", err)
		}
		orphan := f.host.procs[0]
		f.worker.ownerDead = false
		f.expectRelaunchOnce(t, f.crashedKey(t))
		if orphan.alive || orphan.working {
			t.Fatalf("unreceipted held worker was not stopped before reuse: %+v", orphan)
		}
	})
	t.Run("after receipt before attach", func(t *testing.T) {
		f := newLaunchFixture(t)
		f.s.AfterLifecycleEffect = func(kind, _ string) error {
			if kind == "launch" {
				return context.Canceled
			}
			return nil
		}
		if _, err := f.s.Dispatch(t.Context(), "run", "a", "write"); !errors.Is(err, context.Canceled) {
			t.Fatalf("crash not observed: %v", err)
		}
		f.expectHeldWorker(t, f.crashedKey(t), driver.LaunchRunning)
	})
	t.Run("after attach", func(t *testing.T) {
		f := newLaunchFixture(t)
		d, err := f.s.Dispatch(t.Context(), "run", "a", "write")
		if err != nil {
			t.Fatal(err)
		}
		rec, _ := d.Handle.LaunchReceipt()
		f.expectHeldWorker(t, rec.Key, driver.LaunchRunning)
	})
	t.Run("unsupported driver", func(t *testing.T) {
		f := newLaunchFixture(t)
		f.s.Drivers["worker"] = unsupervisedWorker{inner: f.worker.inner}
		if _, err := f.s.Dispatch(t.Context(), "run", "a", "write"); !errors.Is(err, driver.ErrLaunchUnsupported) {
			t.Fatalf("unsupported driver launched: %v", err)
		}
		run, _, _ := f.s.load(t.Context(), "run")
		if run.Operation != nil || run.Tasks[0].State != marshal.Queued {
			t.Fatalf("refused launch left state: op=%+v task=%+v", run.Operation, run.Tasks[0])
		}
		f.expectWorks(t, 0)
	})
	t.Run("ambiguous saved intent", func(t *testing.T) {
		f := newLaunchFixture(t)
		f.journal.crashAt = driver.LaunchReceipted
		f.worker.ownerDead = true
		if _, err := f.s.Dispatch(t.Context(), "run", "a", "write"); !errors.Is(err, context.Canceled) {
			t.Fatalf("crash not observed: %v", err)
		}
		key := f.crashedKey(t)
		f.host.findErr = errors.New("process table unreadable")
		run := f.restart(t)
		if run.Tasks[0].State != marshal.Dispatched || run.Pause == nil || run.Pause.Code != marshal.CodeOperationRecovery {
			t.Fatalf("ambiguous launch released its slot: task=%+v pause=%+v", run.Tasks[0], run.Pause)
		}
		if !f.s.LaunchSupervisor.Quarantined(key) {
			t.Fatal("ambiguous launch output not quarantined")
		}
		f.host.findErr = nil
		if _, err := f.s.LaunchSupervisor.Launch(t.Context(), key, f.worker, driver.Request{RunID: "run", Task: run.Tasks[0], Worktree: t.TempDir(), Brief: "write"}, nil); err == nil {
			t.Fatal("ambiguous key launched under the same key")
		}
		f.expectWorks(t, 0)
	})
	t.Run("stale intent from an earlier boot", func(t *testing.T) {
		f := newLaunchFixture(t)
		f.worker.crashBeforeSpawn = true
		if _, err := f.s.Dispatch(t.Context(), "run", "a", "write"); !errors.Is(err, context.Canceled) {
			t.Fatalf("crash not observed: %v", err)
		}
		f.host.reboot()
		f.expectRelaunchOnce(t, f.crashedKey(t))
	})
	t.Run("stale receipt from an earlier boot", func(t *testing.T) {
		f := newLaunchFixture(t)
		f.s.AfterLifecycleEffect = func(kind, _ string) error {
			if kind == "launch" {
				return context.Canceled
			}
			return nil
		}
		if _, err := f.s.Dispatch(t.Context(), "run", "a", "write"); !errors.Is(err, context.Canceled) {
			t.Fatalf("crash not observed: %v", err)
		}
		f.host.reboot()
		f.expectHeldWorker(t, f.crashedKey(t), driver.LaunchExited)
	})
}

func TestStaleLaunchRecoveryRequiresProvenProcessExit(t *testing.T) {
	f := newLaunchFixture(t)
	f.s.AfterLifecycleEffect = func(kind, _ string) error {
		if kind == "launch" {
			return context.Canceled
		}
		return nil
	}
	if _, err := f.s.Dispatch(t.Context(), "run", "a", "write"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	key := f.crashedKey(t)
	commitFileOnBranch(t, f.s.Repository, "main", "advance.txt", "changed\n", "advance delivery base")
	run := f.restart(t)
	if !run.Tasks[0].StaleProcessExitUnverified || run.Tasks[0].State != marshal.Dispatched || run.Tasks[0].QuarantinedRef != "" || run.Operation == nil {
		t.Fatalf("live worker lost its reservation or worktree protection: %+v", run)
	}
	rec, ok, err := f.journal.Load(key)
	if err != nil || !ok {
		t.Fatalf("receipt: %v %v", ok, err)
	}
	if err := f.host.Stop(rec.Process); err != nil {
		t.Fatal(err)
	}
	_, _ = f.s.Resume(t.Context(), "run")
	run, err = f.s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].StaleProcessExitUnverified || run.Tasks[0].State != marshal.Escalated || run.Operation != nil {
		t.Fatalf("proven exit did not release reservation: %+v", run)
	}
}

func TestStartedProcessExitRequiresLaunchReceiptProof(t *testing.T) {
	for _, name := range []string{"running", "exited", "absent", "unreadable", "unverifiable", "supervisor unavailable", "operation key"} {
		t.Run(name, func(t *testing.T) {
			host := &launchHost{boot: "boot-1"}
			journal := driver.FileLaunchJournal{Dir: t.TempDir()}
			id := driver.ProcessIdentity{BootID: host.boot, PID: 1, Start: "start"}
			host.procs = []*launchProc{{id: id, key: "launch", alive: name != "exited"}}
			rec := driver.LaunchReceipt{Key: "launch", Stage: driver.LaunchReceipted, Process: id}
			if name == "unverifiable" {
				rec.Process = driver.ProcessIdentity{}
			}
			if name != "absent" {
				if err := journal.Store(rec); err != nil {
					t.Fatal(err)
				}
			}
			if name == "unreadable" {
				journal.Dir = filepath.Join(journal.Dir, "invalid")
				if err := os.WriteFile(journal.Dir, []byte("invalid"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s := &MarshalService{LaunchSupervisor: &driver.LaunchSupervisor{Journal: journal, Processes: host}}
			if name == "supervisor unavailable" {
				s.LaunchSupervisor = nil
			}
			// An optional callback cannot override durable evidence of a live worker.
			s.VerifyProcessExit = func(context.Context, string, string) (bool, error) { return true, nil }
			task := marshal.Task{PlanTaskID: "a", LaunchKey: "launch"}
			run := marshal.Run{Tasks: []marshal.Task{task}}
			if name == "operation key" {
				run.Operation = &marshal.LifecycleOperation{Kind: "launch", TaskID: "a", Next: &marshal.Run{Tasks: []marshal.Task{task}}}
				task.LaunchKey = ""
			}
			exited, err := s.verifyStartedProcessExited(t.Context(), "run", run, task)
			if exited != (name == "exited") {
				t.Fatalf("exit=%v error=%v", exited, err)
			}
		})
	}
}
