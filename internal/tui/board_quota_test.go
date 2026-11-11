package tui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
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

// TestQuotaCardShown: the all-providers-at-limit pause shows the owner card
// first in the inbox, with its exact line and keys.
func TestQuotaCardShown(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.setMarshalPanel(quotaPanel(time.Now().Add(90 * time.Minute)))
	got := plainFrame(w, 170, 45)
	if !strings.Contains(got, "⚑ limits · All providers are at their limit. Work is saved.") {
		t.Fatalf("quota card line missing:\n%s", got)
	}
	if !strings.Contains(got, "[r] resume when available  [s] stop here") {
		t.Fatalf("quota card keys missing:\n%s", got)
	}
	// The generic stop command is not shown for the quota pause.
	if strings.Contains(got, "type › /marshal resume") {
		t.Fatalf("quota pause fell back to the generic stop entry:\n%s", got)
	}
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

// TestQuotaCardStopDisarms: s disarms auto-resume, records the plain line,
// and leaves the card on its resume key.
func TestQuotaCardStopDisarms(t *testing.T) {
	w, _ := boardWorkspace(t)
	until := time.Now().Add(time.Hour)
	w.setMarshalPanel(quotaPanel(until))

	w.quotaCard.freeFn = func() bool { return false }
	w.quotaCard.waitFn = func(ctx context.Context, _ time.Time) bool { <-ctx.Done(); return false }
	w.quotaCard.resumeFn = func(context.Context) { t.Error("resume ran after stop") }

	w.armQuotaResume(context.Background(), until)
	if !w.quotaCard.isArmed() {
		t.Fatal("arm failed")
	}
	// Armed, the card's second row says it will resume.
	if got := plainFrame(w, 170, 45); !strings.Contains(got, "Resuming when a limit frees up") {
		t.Fatalf("armed card missing resume line:\n%s", got)
	}

	run := boardmodel.RunHead{State: marshal.AwaitingUser, PauseKind: "quota", PauseUntil: until}
	if !w.handleQuotaKey(KeyEvent{Type: KeyRune, Rune: 's'}, run) {
		t.Fatal("s not handled on the quota card")
	}

	deadline := time.Now().Add(2 * time.Second)
	for w.quotaCard.isArmed() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if w.quotaCard.isArmed() {
		t.Fatal("s did not disarm auto-resume")
	}

	foundActivity := false
	for _, n := range w.noteList() {
		if strings.Contains(n.Text, "Stopped here. The work is saved; press r on the card to resume later.") {
			foundActivity = true
		}
	}
	if !foundActivity {
		t.Fatalf("stop activity line not recorded: %v", w.noteList())
	}

	// The card stays, back on its resume key.
	if got := plainFrame(w, 170, 45); !strings.Contains(got, "[r] resume when available") {
		t.Fatalf("card did not return to the resume key:\n%s", got)
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
