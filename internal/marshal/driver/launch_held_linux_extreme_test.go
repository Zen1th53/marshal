//go:build linux && !governed

package driver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func heldNative(marker string) Native {
	return Native{Provider: "fake", Binary: "/bin/sh", Parse: parseNone, Args: func(Request) []string {
		return []string{"-c", `echo run >> "` + marker + `"`}
	}}
}

func heldRequest(t *testing.T) Request {
	return Request{RunID: "run", Task: marshal.Task{PlanTaskID: "a", BaseCommit: "base", Worker: "fake"}, Worktree: t.TempDir(), Brief: "write"}
}

func waitDone(t *testing.T, h *Handle) {
	t.Helper()
	select {
	case <-h.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestNativeHeldLaunchDoesNoWorkBeforeRelease(t *testing.T) {
	req := heldRequest(t)
	marker := filepath.Join(t.TempDir(), "marker")
	start, err := heldNative(marker).LaunchHeld(context.Background(), req, "held-key-1")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("worker ran before release: %v", err)
	}
	found, err := HostProcesses().Find("held-key-1")
	if err != nil || len(found) != 1 || found[0] != start.Process {
		t.Fatalf("held process not rediscovered by key: %+v %v (want %+v)", found, err, start.Process)
	}
	h, err := start.Release()
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, h)
	if data, _ := os.ReadFile(marker); strings.Count(string(data), "run") != 1 {
		t.Fatalf("released worker output %q", data)
	}
}

func TestNativeHeldLaunchAbortProvesExitWithoutWork(t *testing.T) {
	req := heldRequest(t)
	marker := filepath.Join(t.TempDir(), "marker")
	start, err := heldNative(marker).LaunchHeld(context.Background(), req, "held-key-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := start.Abort(); err != nil {
		t.Fatal(err)
	}
	if alive, err := HostProcesses().Alive(start.Process); err != nil || alive {
		t.Fatalf("aborted worker alive=%v err=%v", alive, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("aborted worker ran: %v", err)
	}
}

func TestNativeSupervisedLaunchNeverRunsTwice(t *testing.T) {
	req := heldRequest(t)
	marker := filepath.Join(t.TempDir(), "marker")
	journal := FileLaunchJournal{Dir: filepath.Join(t.TempDir(), "launches")}
	sup := &LaunchSupervisor{Journal: journal, Processes: HostProcesses()}
	h, err := sup.Launch(context.Background(), "held-key-3", heldNative(marker), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok, err := journal.Load("held-key-3")
	if err != nil || !ok || rec.Stage != LaunchReceipted || !rec.Process.verifiable() {
		t.Fatalf("receipt %+v %v %v", rec, ok, err)
	}
	waitDone(t, h)
	// A restarted owner sees the receipted worker exited and refuses to
	// launch the same key again.
	restarted := &LaunchSupervisor{Journal: journal, Processes: HostProcesses()}
	if _, err := restarted.Launch(context.Background(), "held-key-3", heldNative(marker), req, nil); heldDisposition(err) != LaunchExited {
		t.Fatalf("receipted launch ran again: %v", err)
	}
	if data, _ := os.ReadFile(marker); strings.Count(string(data), "run") != 1 {
		t.Fatalf("worker ran %q", data)
	}
}

func TestNativeSupervisedLaunchReusesKeyAfterUnreceiptedCrash(t *testing.T) {
	req := heldRequest(t)
	marker := filepath.Join(t.TempDir(), "marker")
	journal := FileLaunchJournal{Dir: filepath.Join(t.TempDir(), "launches")}
	boot, err := HostProcesses().BootID()
	if err != nil {
		t.Fatal(err)
	}
	// The owner wrote the intent and spawned the held worker, then stopped
	// before the receipt.
	if err := journal.Store(LaunchReceipt{Key: "held-key-4", Stage: LaunchIntent, BootID: boot}); err != nil {
		t.Fatal(err)
	}
	orphan, err := heldNative(marker).LaunchHeld(context.Background(), req, "held-key-4")
	if err != nil {
		t.Fatal(err)
	}
	sup := &LaunchSupervisor{Journal: journal, Processes: HostProcesses()}
	h, err := sup.Launch(context.Background(), "held-key-4", heldNative(marker), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, h)
	if alive, _ := HostProcesses().Alive(orphan.Process); alive {
		t.Fatal("unreceipted held worker survived relaunch")
	}
	_ = orphan.Abort()
	if data, _ := os.ReadFile(marker); strings.Count(string(data), "run") != 1 {
		t.Fatalf("worker ran %q", data)
	}
}
