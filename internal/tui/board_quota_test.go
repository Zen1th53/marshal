//go:build linux

package tui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// quotaPanel builds a run paused because every provider is at its limit.
func quotaPanel(until time.Time) *MarshalPanel {
	run := fakeRun()
	run.State = marshal.AwaitingUser
	run.Pause = &marshal.Pause{
		Reason:      "provider failover: waiting_on_quota",
		Resolutions: []string{"resume after the provider quota resets"},
		Code:        marshal.CodeProviderQuota,
		Action:      marshal.PauseResume,
		Kind:        "quota",
		Until:       until,
	}
	return newMarshalPanel("R-7f3a", "codex", run, "waiting for a provider limit to free up")
}

// TestQuotaCardResumeArmsAndFiresOnce: r arms auto-resume; the loop resumes
// exactly once when the reset time arrives, then disarms.
func TestQuotaCardResumeArmsAndFiresOnce(t *testing.T) {
	w, _ := boardWorkspace(t)

	var mu sync.Mutex
	now := time.Now()
	until := now.Add(time.Hour)
	setNow := func(tm time.Time) { mu.Lock(); now = tm; mu.Unlock() }
	getNow := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }

	resumed := make(chan struct{}, 4)
	var resumes int32

	w.quotaCard.nowFn = getNow
	w.quotaCard.freeFn = func() bool { return false }
	// A poll finds no free provider yet, but the reset time has now arrived:
	// the loop's next check fires the resume. The fake clock drives it, so
	// nothing depends on wall time.
	w.quotaCard.waitFn = func(ctx context.Context, _ time.Time) bool {
		if ctx.Err() != nil {
			return false
		}
		setNow(until)
		return true
	}
	w.quotaCard.resumeFn = func(context.Context) {
		atomic.AddInt32(&resumes, 1)
		resumed <- struct{}{}
	}

	w.armQuotaResume(context.Background(), until)
	if !w.quotaCard.isArmed() {
		t.Fatal("r did not arm auto-resume")
	}

	select {
	case <-resumed:
	case <-time.After(2 * time.Second):
		t.Fatal("resume was not called at the reset time")
	}

	// It disarms after resuming and never resumes again.
	deadline := time.Now().Add(2 * time.Second)
	for w.quotaCard.isArmed() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if w.quotaCard.isArmed() {
		t.Fatal("auto-resume did not disarm after firing")
	}
	time.Sleep(10 * time.Millisecond)
	if n := atomic.LoadInt32(&resumes); n != 1 {
		t.Fatalf("resume called %d times, want exactly 1", n)
	}
}

// TestActivityCollapsesReroutes: two reroutes of the same provider pair
// within a minute collapse into one line with the summed count.
func TestActivityCollapsesReroutes(t *testing.T) {
	w, _ := boardWorkspace(t)
	before := len(w.noteList())

	w.RecordActivity(rerouteActivityLine("codex", 1, "claude"))
	w.RecordActivity(rerouteActivityLine("codex", 1, "claude"))

	notes := w.noteList()
	if len(notes) != before+1 {
		t.Fatalf("reroutes did not collapse: %d new notes, want 1\n%v", len(notes)-before, notes)
	}
	if !strings.Contains(notes[0].Text, "codex reached its limit — I moved 2 tasks to claude.") {
		t.Fatalf("collapsed line wrong: %q", notes[0].Text)
	}

	// A different pair does not collapse into it.
	w.RecordActivity(rerouteActivityLine("agy", 1, "opencode"))
	if got := w.noteList(); len(got) != before+2 {
		t.Fatalf("a different pair collapsed: %d new notes\n%v", len(got)-before, got)
	}
}
