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

// fakeDraftWorkers installs stand-ins for the live Mac demo's workers.
func fakeDraftWorkers(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	for _, worker := range []string{"codex", "claude", "agy"} {
		if err := os.WriteFile(filepath.Join(bin, worker), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

const draftRejectTestProject = "PROJECT-11111111111111111111111111111111"

// refusedDraftSecret is draft text that must never leave the runtime.
const refusedDraftSecret = "UNTRUSTED-DRAFT-TEXT-7f3a"

// refusedDraft is refused on every platform and edition: its second task
// has a mode outside the schema.
func refusedDraft(t *testing.T) error {
	t.Helper()
	fakeDraftWorkers(t)
	task := `{"id":"%s","title":"T ` + refusedDraftSecret + `","criteria":["C ` + refusedDraftSecret + `"],"paths":["p/` + refusedDraftSecret + `"],"depends_on":[],"worker":"codex","mode":"%s","control":"free","checks":[{"command":"true ` + refusedDraftSecret + `","criteria":["C ` + refusedDraftSecret + `"]}]}`
	data := []byte(`{"tasks":[` + strings.Replace(strings.Replace(task, "%s", "one", 1), "%s", "governed", 1) + `,` + strings.Replace(strings.Replace(task, "%s", "two", 1), "%s", "mode-"+refusedDraftSecret, 1) + `]}`)
	_, err := (&app.MarshalService{ProjectID: draftRejectTestProject}).DraftFromProposal(data, "claude")
	if err == nil {
		t.Fatal("invalid draft imported")
	}
	return err
}

// The live Mac demo: the runtime refused the Marshal's draft, wrote a fixed
// line to Activity and the inbox, never told the Marshal why or woke it, and
// the Board said STARTING with nothing from the owner. A refusal now wakes
// the Marshal with the field and rule, and the Board shows the plan is being
// fixed until a draft is imported, then asks the owner past the bound.
func TestRefusedDraftWakesMarshalAndShowsOnBoard(t *testing.T) {
	err := refusedDraft(t)
	w := emptyBoardWorkspace(t)
	var told []string
	w.draftTell = func(text string) { told = append(told, text) }
	m := w.marshalSession()
	m.mu.Lock()
	m.runID = "RUN-watched"
	m.mu.Unlock()
	w.marshalDraftRejected(m, "RUN-watched", "claude", "draft validation failed", err)

	const reason = "tasks[1].mode: must be native or governed"
	if len(told) != 1 || !strings.HasPrefix(told[0], marshalNotePrefix+"The runtime refused your plan draft. Reason: "+reason+".") {
		t.Fatalf("Marshal not woken with the reason: %q", told)
	}
	// The pack and the draft were both taken: the notice asks for both.
	if !strings.Contains(told[0], app.MarshalPackRelativePath+"/") || !strings.Contains(told[0], marshalDraftRelativePath+".ready") {
		t.Fatalf("notice does not say what to republish: %q", told[0])
	}
	inbox, readErr := os.ReadFile(inboxPath(w.workDir, "marshal"))
	if readErr != nil || !strings.Contains(string(inbox), "Reason: "+reason) {
		t.Fatalf("inbox note has no reason (%v):\n%s", readErr, inbox)
	}
	for _, text := range append([]string{string(inbox), w.marshalPanel().Note}, told...) {
		if strings.Contains(text, refusedDraftSecret) {
			t.Fatalf("draft text reached the Marshal, the inbox or the Board:\n%s", text)
		}
	}

	in := w.flowInputs()
	if state, _ := flowState(in); state != "FIXING PLAN" {
		t.Fatalf("top bar state %q, want FIXING PLAN", state)
	}
	if doing := flowDoing(in); !strings.HasPrefix(doing, "fixing the plan the runtime refused") {
		t.Fatalf("Marshal line %q", doing)
	}
	if from, ok := fromYou(in); from != "nothing · next ask: "+planNextAsk() || !ok {
		t.Fatalf("from you %q %v, want the plan as the next ask", from, ok)
	}

	// Past the bound without a fix the owner is asked.
	w.draftReject.mu.Lock()
	w.draftReject.mark.since = time.Now().Add(-draftFixBound - time.Minute)
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

// A refusal of a superseding draft on another run never hides a running
// run's state.
func TestRefusedDraftOnAnotherRunLeavesTheRun(t *testing.T) {
	in := flowIn{hasRun: true, now: time.Now(), panel: &MarshalPanel{RunID: "RUN-live"}, v: boardVisual{board: boardWithRun(marshal.Dispatching, false)},
		draftRejected: &draftRejectMark{since: time.Now().Add(-time.Hour), runID: "RUN-old"}}
	if in.fixingPlan() {
		t.Fatal("a refusal on another run took over the Board")
	}
	in.panel.RunID, in.draftRejected.runID = "RUN-live", "RUN-live"
	if in.fixingPlan() {
		t.Fatal("a refused replan hid a dispatching run")
	}
	for _, a := range ownerAsks(in) {
		if strings.Contains(a.line, "plan refused") {
			t.Fatal("a refused replan asked the owner during a running run")
		}
	}
}

// A runtime-side refusal (pack, evidence) is not repeated in the chat: the
// notice points at the inbox note, which carries the redacted reason.
func TestRuntimeSideRefusalPointsAtInbox(t *testing.T) {
	w := emptyBoardWorkspace(t)
	var told []string
	w.draftTell = func(text string) { told = append(told, text) }
	m := w.marshalSession()
	m.mu.Lock()
	m.runID = "RUN-watched"
	m.mu.Unlock()
	w.marshalDraftRejected(m, "RUN-watched", "claude", "plan pack validation failed", os.ErrNotExist)
	if len(told) != 1 || strings.Contains(told[0], "Reason:") || !strings.Contains(told[0], "read the reason in "+marshalInboxFile) {
		t.Fatalf("runtime-side refusal notice: %q", told)
	}
	if w.draftRejectedNow() == nil {
		t.Fatal("refusal not marked on the Board")
	}
}

// A refusal with no open chat writes the inbox note only; the notice path is
// not started without a chat to deliver to.
func TestRefusedDraftWithoutChatWritesInboxOnly(t *testing.T) {
	err := refusedDraft(t)
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
	fakeDraftWorkers(t)
	brief, err := marshalRoleBriefing(app.MarshalWorkers("claude"), marshal.DefaultSettings(), marshal.Standard)
	if err != nil {
		t.Fatal(err)
	}
	offered := "- Workers you may assign tasks to: codex, claude, agy.\n"
	if !app.AGYAssignable() {
		offered = "- Workers you may assign tasks to: codex, claude.\n"
		if strings.Contains(brief, "AGY supports governed") {
			t.Fatal("briefing says agy supports governed tasks where it does not")
		}
	}
	if !strings.Contains(brief, offered) {
		t.Fatalf("briefing does not offer exactly %q", offered)
	}
}
