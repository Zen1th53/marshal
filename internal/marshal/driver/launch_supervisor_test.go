package driver

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

type fakeProc struct {
	id      ProcessIdentity
	key     string
	alive   bool
	working bool
}

// fakeHost is a portable process table: a held process is alive but does no
// work until its barrier is released.
type fakeHost struct {
	boot     string
	next     int
	procs    []*fakeProc
	findErr  error
	aliveErr error
	stopErr  error
}

func (h *fakeHost) BootID() (string, error) { return h.boot, nil }
func (h *fakeHost) Find(key string) ([]ProcessIdentity, error) {
	if h.findErr != nil {
		return nil, h.findErr
	}
	var out []ProcessIdentity
	for _, p := range h.procs {
		if p.alive && p.key == key && p.id.BootID == h.boot {
			out = append(out, p.id)
		}
	}
	return out, nil
}
func (h *fakeHost) proc(id ProcessIdentity) *fakeProc {
	for _, p := range h.procs {
		if p.id == id {
			return p
		}
	}
	return nil
}
func (h *fakeHost) Alive(id ProcessIdentity) (bool, error) {
	if h.aliveErr != nil {
		return false, h.aliveErr
	}
	p := h.proc(id)
	return p != nil && p.alive && id.BootID == h.boot, nil
}
func (h *fakeHost) Stop(id ProcessIdentity) error {
	if h.stopErr != nil {
		return h.stopErr
	}
	if p := h.proc(id); p != nil {
		p.alive = false
	}
	return nil
}
func (h *fakeHost) working() int {
	n := 0
	for _, p := range h.procs {
		if p.working {
			n++
		}
	}
	return n
}

type fakeHeldDriver struct {
	host      *fakeHost
	journal   LaunchJournal
	noProcess bool
	badID     bool
	// sawReceipt records whether the receipt was durable at release.
	sawReceipt bool
}

func (d *fakeHeldDriver) Mode() marshal.WorkerMode { return marshal.Native }
func (d *fakeHeldDriver) Launch(context.Context, Request) (*Handle, error) {
	return nil, errors.New("unsupervised launch")
}
func (d *fakeHeldDriver) Wait(context.Context, *Handle) (marshal.HandIn, error) {
	return marshal.HandIn{}, nil
}
func (d *fakeHeldDriver) Cancel(*Handle) error { return nil }
func (d *fakeHeldDriver) LaunchHeld(_ context.Context, req Request, key string) (*HeldStart, error) {
	if d.noProcess {
		return nil, context.Canceled
	}
	d.host.next++
	p := &fakeProc{id: ProcessIdentity{BootID: d.host.boot, PID: d.host.next, Start: "t"}, key: key, alive: true}
	if d.badID {
		p.id.Start = ""
	}
	d.host.procs = append(d.host.procs, p)
	return &HeldStart{Process: p.id, Release: func() (*Handle, error) {
		rec, ok, _ := d.journal.Load(key)
		d.sawReceipt = ok && rec.Stage == LaunchReceipted && rec.Process == p.id
		p.working = true
		return NewHandle(req), nil
	}, Abort: func() error { p.alive = false; return nil }}, nil
}

type noBootHost struct{ *fakeHost }

func (noBootHost) BootID() (string, error) { return "", ErrLaunchUnsupported }

type plainDriver struct{}

func (plainDriver) Mode() marshal.WorkerMode { return marshal.Native }
func (plainDriver) Launch(context.Context, Request) (*Handle, error) {
	return nil, errors.New("launched")
}
func (plainDriver) Wait(context.Context, *Handle) (marshal.HandIn, error) {
	return marshal.HandIn{}, nil
}
func (plainDriver) Cancel(*Handle) error { return nil }

func supervisorFixture(t *testing.T) (*LaunchSupervisor, *fakeHost, *fakeHeldDriver, Request) {
	t.Helper()
	host := &fakeHost{boot: "boot-1"}
	journal := FileLaunchJournal{Dir: filepath.Join(t.TempDir(), "launches")}
	sup := &LaunchSupervisor{Journal: journal, Processes: host}
	req := Request{RunID: "run", Task: marshal.Task{PlanTaskID: "a", BaseCommit: "base", Worker: "codex"}, Worktree: t.TempDir(), Brief: "write"}
	return sup, host, &fakeHeldDriver{host: host, journal: journal}, req
}

func heldDisposition(err error) LaunchDisposition {
	var held *LaunchHeldError
	if errors.As(err, &held) {
		return held.Disposition
	}
	return ""
}

func TestLaunchSupervisorReceiptPrecedesWork(t *testing.T) {
	sup, host, d, req := supervisorFixture(t)
	h, err := sup.Launch(context.Background(), "k1", d, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !d.sawReceipt || host.working() != 1 {
		t.Fatalf("work started before a durable receipt: saw=%v working=%d", d.sawReceipt, host.working())
	}
	if rec, ok := h.LaunchReceipt(); !ok || rec.Key != "k1" || rec.SessionID != "k1" {
		t.Fatalf("handle receipt %+v %v", rec, ok)
	}
	if err := sup.Attached("k1", "k1"); err != nil {
		t.Fatal(err)
	}
	// A restarted owner reconnects; it never launches the key again.
	restarted := &LaunchSupervisor{Journal: sup.Journal, Processes: host}
	if _, err := restarted.Launch(context.Background(), "k1", d, req, nil); heldDisposition(err) != LaunchRunning {
		t.Fatalf("relaunch of attached key: %v", err)
	}
	if host.working() != 1 {
		t.Fatalf("worker duplicated: %d", host.working())
	}
}

func TestLaunchSupervisorBlockedProcessIsStoppedBeforeReuse(t *testing.T) {
	sup, host, d, req := supervisorFixture(t)
	// The owner died after spawning but before the receipt.
	_ = sup.Journal.Store(LaunchReceipt{Key: "k1", Stage: LaunchIntent, BootID: "boot-1"})
	host.procs = append(host.procs, &fakeProc{id: ProcessIdentity{BootID: "boot-1", PID: 77, Start: "s"}, key: "k1", alive: true})
	admitted := false
	if _, err := sup.Launch(context.Background(), "k1", d, req, func(context.Context) error { admitted = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if host.procs[0].alive || host.procs[0].working || !admitted || host.working() != 1 {
		t.Fatalf("blocked process not stopped before reuse: %+v admitted=%v", host.procs[0], admitted)
	}
}

func TestLaunchSupervisorAmbiguousAndStale(t *testing.T) {
	t.Run("discovery fails", func(t *testing.T) {
		sup, host, d, req := supervisorFixture(t)
		_ = sup.Journal.Store(LaunchReceipt{Key: "k1", Stage: LaunchIntent, BootID: "boot-1"})
		host.findErr = errors.New("proc unreadable")
		if _, err := sup.Launch(context.Background(), "k1", d, req, nil); heldDisposition(err) != LaunchAmbiguous {
			t.Fatalf("ambiguous intent launched: %v", err)
		}
		host.findErr = nil
		if !sup.Quarantined("k1") {
			t.Fatal("ambiguous launch not quarantined")
		}
		if _, err := sup.Launch(context.Background(), "k1", d, req, nil); heldDisposition(err) != LaunchAmbiguous || host.working() != 0 {
			t.Fatalf("quarantine lifted by itself: %v", err)
		}
	})
	t.Run("two processes", func(t *testing.T) {
		sup, host, d, req := supervisorFixture(t)
		_ = sup.Journal.Store(LaunchReceipt{Key: "k1", Stage: LaunchIntent, BootID: "boot-1"})
		for pid := 1; pid <= 2; pid++ {
			host.procs = append(host.procs, &fakeProc{id: ProcessIdentity{BootID: "boot-1", PID: pid, Start: "s"}, key: "k1", alive: true})
		}
		if disp, _ := sup.Inspect("k1"); disp != LaunchAmbiguous {
			t.Fatalf("disposition %s", disp)
		}
		if _, err := sup.Launch(context.Background(), "k1", d, req, nil); heldDisposition(err) != LaunchAmbiguous {
			t.Fatal(err)
		}
	})
	t.Run("stale intent from an earlier boot", func(t *testing.T) {
		sup, host, d, req := supervisorFixture(t)
		_ = sup.Journal.Store(LaunchReceipt{Key: "k1", Stage: LaunchIntent, BootID: "boot-0"})
		if disp, _ := sup.Inspect("k1"); disp != LaunchAbsent {
			t.Fatalf("disposition %s", disp)
		}
		if _, err := sup.Launch(context.Background(), "k1", d, req, nil); err != nil || host.working() != 1 {
			t.Fatalf("stale intent did not relaunch the same key once: %v", err)
		}
	})
	t.Run("stale receipt from an earlier boot", func(t *testing.T) {
		sup, host, d, req := supervisorFixture(t)
		_ = sup.Journal.Store(LaunchReceipt{Key: "k1", Stage: LaunchReceipted, BootID: "boot-0", Process: ProcessIdentity{BootID: "boot-0", PID: 5, Start: "s"}, SessionID: "k1"})
		if _, err := sup.Launch(context.Background(), "k1", d, req, nil); heldDisposition(err) != LaunchExited || host.working() != 0 {
			t.Fatalf("a receipted worker was launched again: %v", err)
		}
	})
	t.Run("identity check fails", func(t *testing.T) {
		sup, host, d, req := supervisorFixture(t)
		_ = sup.Journal.Store(LaunchReceipt{Key: "k1", Stage: LaunchReceipted, BootID: "boot-1", Process: ProcessIdentity{BootID: "boot-1", PID: 5, Start: "s"}, SessionID: "k1"})
		host.aliveErr = errors.New("unreadable")
		if _, err := sup.Launch(context.Background(), "k1", d, req, nil); heldDisposition(err) != LaunchAmbiguous {
			t.Fatal(err)
		}
	})
	t.Run("session differs", func(t *testing.T) {
		sup, _, d, req := supervisorFixture(t)
		if _, err := sup.Launch(context.Background(), "k1", d, req, nil); err != nil {
			t.Fatal(err)
		}
		if err := sup.Attached("k1", "other"); heldDisposition(err) != LaunchAmbiguous || !sup.Quarantined("k1") {
			t.Fatalf("session mismatch adopted: %v", err)
		}
	})
}

func TestLaunchSupervisorUnsupported(t *testing.T) {
	sup, host, d, req := supervisorFixture(t)
	if _, err := sup.Launch(context.Background(), "k1", plainDriver{}, req, nil); !errors.Is(err, ErrLaunchUnsupported) {
		t.Fatalf("driver without barrier: %v", err)
	}
	d.badID = true
	if _, err := sup.Launch(context.Background(), "k2", d, req, nil); !errors.Is(err, ErrLaunchUnsupported) || host.working() != 0 || host.procs[0].alive {
		t.Fatalf("unverifiable identity: %v", err)
	}
	inside := &LaunchSupervisor{Journal: FileLaunchJournal{Dir: filepath.Join(req.Worktree, ".launch")}, Processes: host}
	if _, err := inside.Launch(context.Background(), "k3", &fakeHeldDriver{host: host, journal: inside.Journal}, req, nil); !errors.Is(err, ErrLaunchUnsupported) {
		t.Fatalf("receipts inside worktree: %v", err)
	}
	if _, err := (&LaunchSupervisor{Journal: sup.Journal, Processes: noBootHost{host}}).Launch(context.Background(), "k4", d, req, nil); !errors.Is(err, ErrLaunchUnsupported) {
		t.Fatalf("platform without identity: %v", err)
	}
}

// A governed driver not opted into holding, or on a platform or edition
// without a startup barrier, is not supervised and never starts its run
// from a held launch.
func TestGovernedHoldUnavailableRefusesBeforeRunning(t *testing.T) {
	ran := false
	g := Governed{Run: func(context.Context, Request) ([]marshal.CommandRecord, error) { ran = true; return nil, nil }}
	if HoldsLaunch(g) {
		t.Fatal("governed driver without Held is supervised")
	}
	if _, err := g.LaunchHeld(t.Context(), Request{}, "key"); !errors.Is(err, ErrLaunchUnsupported) {
		t.Fatalf("held launch of an unheld driver: %v", err)
	}
	if ran {
		t.Fatal("refused held launch started the run")
	}
	if g.Held = true; HoldsLaunch(g) != governedHoldAvailable() {
		t.Fatal("held governed driver ignores the platform barrier")
	}
}
