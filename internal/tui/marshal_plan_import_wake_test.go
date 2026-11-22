package tui

import (
	"os"
	"strings"
	"testing"
)

// The live Mac demo: the Marshal published its draft and said "Once the
// runtime accepts the draft, I'll send the approval"; the runtime imported
// it, wrote "the Marshal approved it and the tasks start now" to Activity,
// and told the Marshal nothing, so no approval ever came. An import with no
// approval pending now wakes the Marshal, names the approve proposal, leaves
// the same note in its inbox, and Activity no longer claims an approval.
func TestImportedPlanWithoutApprovalWakesMarshal(t *testing.T) {
	w := emptyBoardWorkspace(t)
	var told []string
	w.draftTell = func(text string) { told = append(told, text) }
	activity := w.afterPlanImported("RUN-1795320601100625000", 4)

	if len(told) != 1 || !strings.HasPrefix(told[0], marshalNotePrefix+"The runtime imported your plan draft: run RUN-1795320601100625000 with 4 tasks.") || !strings.Contains(told[0], `{"action":"approve"}`) {
		t.Fatalf("Marshal not woken for the approval: %q", told)
	}
	if strings.Contains(activity, "approved it") || strings.Contains(activity, "start now") {
		t.Fatalf("Activity claims an approval that never happened: %q", activity)
	}
	inbox, err := os.ReadFile(inboxPath(w.workDir, "marshal"))
	if err != nil || !strings.Contains(string(inbox), "plan imported") || !strings.Contains(string(inbox), `{"action":"approve"}`) {
		t.Fatalf("inbox has no plan-imported note (%v):\n%s", err, inbox)
	}
}

// An approval the Marshal sent before the import is the one applied: it is
// not asked for again. (Queueing it needs the stored run; without one the
// approval stays pending and the Marshal is asked.)
func TestPendingApprovalNotQueuedWithoutRunAsksMarshal(t *testing.T) {
	w := emptyBoardWorkspace(t)
	var told []string
	w.draftTell = func(text string) { told = append(told, text) }
	w.permissions.mu.Lock()
	w.permissions.planApprovalPending = true
	w.permissions.mu.Unlock()
	if w.queueMarshalPlanApproval() {
		t.Fatal("an approval was reported queued with no imported run")
	}
	w.afterPlanImported("RUN-x", 1)
	if len(told) != 1 {
		t.Fatalf("Marshal not asked while its approval could not be queued: %q", told)
	}
}
