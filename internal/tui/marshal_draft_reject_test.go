package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// demoRejectedDraft is the live Mac demo's refused draft and the refusal
// DraftFromProposal gives it with the demo's installed workers.
func demoRejectedDraft(t *testing.T) error {
	t.Helper()
	bin := t.TempDir()
	for _, worker := range []string{"codex", "claude", "agy"} {
		if err := os.WriteFile(filepath.Join(bin, worker), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	data, err := os.ReadFile(filepath.Join("..", "app", "testdata", "marshal_demo_agy_governed_draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&app.MarshalService{ProjectID: "PROJECT-1a373820568b8f191e75a9671cc9e0a3"}).DraftFromProposal(data, "claude")
	return err
}

// The live Mac demo: the runtime refused the Marshal's draft, wrote a fixed
// line to Activity and the inbox, never told the Marshal why or woke it, and
// the Board said STARTING with nothing from the owner. A refusal now wakes
// the Marshal with the field and rule, and the Board shows the plan is being
// fixed until a draft is imported, then asks the owner past the bound.
func TestRefusedDraftWakesMarshalAndShowsOnBoard(t *testing.T) {
	err := demoRejectedDraft(t)
	if err == nil {
		t.Skip("agy runs governed here: the demo draft imports")
	}
	w := emptyBoardWorkspace(t)
	var told []string
	w.draftTell = func(text string) { told = append(told, text) }
	m := w.marshalSession()
	m.mu.Lock()
	m.runID = "RUN-watched"
	m.mu.Unlock()
	w.marshalDraftRejected(m, "RUN-watched", "claude", "draft validation failed", err)

	if len(told) != 1 || !strings.HasPrefix(told[0], marshalNotePrefix+"The runtime refused your plan draft. Reason: tasks[1].mode: worker agy does not support governed mode") || !strings.Contains(told[0], "plan-draft.json.ready") {
		t.Fatalf("Marshal not woken with the reason: %q", told)
	}
	inbox, readErr := os.ReadFile(inboxPath(w.workDir, "marshal"))
	if readErr != nil || !strings.Contains(string(inbox), "Reason: tasks[1].mode:") {
		t.Fatalf("inbox note has no reason (%v):\n%s", readErr, inbox)
	}
	for _, leaked := range []string{"Completed task", "due,omitempty", "TestIsOverdue", "Add task due dates", "due-dates", "GOCACHE"} {
		for _, text := range append([]string{string(inbox)}, told...) {
			if strings.Contains(text, leaked) {
				t.Fatalf("draft text %q reached the Marshal or the inbox:\n%s", leaked, text)
			}
		}
	}

	in := w.flowInputs()
	if state, _ := flowState(in); state != "FIXING PLAN" {
		t.Fatalf("top bar state %q, want FIXING PLAN", state)
	}
	if doing := flowDoing(in); !strings.HasPrefix(doing, "fixing the plan the runtime refused") {
		t.Fatalf("Marshal line %q", doing)
	}
	if from, ok := fromYou(in); from != "nothing · next ask: plan approval" || !ok {
		t.Fatalf("from you %q %v, want the plan as the next ask", from, ok)
	}

	// Past the bound without a fix the owner is asked.
	w.draftReject.mu.Lock()
	w.draftReject.since = time.Now().Add(-draftFixBound - time.Minute)
	w.draftReject.mu.Unlock()
	in = w.flowInputs()
	if state, _ := flowState(in); state != "YOUR TURN" {
		t.Fatalf("overdue state %q, want YOUR TURN", state)
	}
	if from, ok := fromYou(in); ok || !strings.Contains(from, "plan refused") || !strings.Contains(from, "open the Marshal chat") {
		t.Fatalf("overdue refusal not asked of the owner: %q %v", from, ok)
	}

	w.clearDraftRejected()
	if w.flowInputs().draftRejected != nil {
		t.Fatal("an imported draft left the refusal on the Board")
	}
}

// A refusal with no open chat writes the inbox note only; the notice path is
// not started without a chat to deliver to.
func TestRefusedDraftWithoutChatWritesInboxOnly(t *testing.T) {
	err := demoRejectedDraft(t)
	if err == nil {
		t.Skip("agy runs governed here: the demo draft imports")
	}
	w := emptyBoardWorkspace(t)
	m := w.marshalSession()
	m.mu.Lock()
	m.runID = "RUN-watched"
	m.mu.Unlock()
	w.marshalDraftRejected(m, "RUN-watched", "claude", "draft validation failed", err)
	if w.draftRejectedNow() == nil {
		t.Fatal("refusal not marked on the Board")
	}
	w.say.mu.Lock()
	sent := w.say.next
	w.say.mu.Unlock()
	if sent != 0 {
		t.Fatalf("a notice was queued with no chat open: %d", sent)
	}
}

// The briefing offers only workers a draft may name: where agy cannot take a
// task, the Marshal is not told it may assign one to it.
func TestBriefingOffersOnlyAssignableWorkers(t *testing.T) {
	if err := demoRejectedDraft(t); err == nil || app.AGYAssignable() {
		t.Skip("agy can take tasks here")
	}
	brief, err := marshalRoleBriefing(app.MarshalWorkers("claude"), marshal.DefaultSettings(), marshal.Standard)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief, "- Workers you may assign tasks to: codex, claude.\n") {
		t.Fatal("briefing still offers agy where every agy task is refused")
	}
	if strings.Contains(brief, "AGY supports governed") {
		t.Fatal("briefing says agy supports governed tasks where it does not")
	}
}
