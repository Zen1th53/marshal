//go:build !governed

package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// A live MCP run proposal is applied once. A second queueing of the same
// bound request (proposal recovery reading the row before the first
// application resolved it) must not be routed again: that wrote a second
// "Marshal allowed: …" Activity line reading "application failed".
func TestLiveMCPRunProposalRecordedOnce(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	service := w.runtime.Marshal()
	m := w.marshalSession()
	m.mu.Lock()
	m.service = service
	m.runID = "RUN-once"
	m.mu.Unlock()
	run := marshal.Run{PlanID: "PLAN-once", PlanVersion: 1, State: marshal.Closed, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "calc-add", State: marshal.Merged, ResultCommit: "result"}}}
	if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, "RUN-once", run, 0); err != nil {
		t.Fatal(err)
	}
	w.setMarshalPanel(newMarshalPanel("RUN-once", "codex", run, "done"))
	if r := submitMCP(t, cfg, `{"submission_id":"amend-once","proposal":{"action":"amend","reason":"remove the cache files"}}`); r.State != "admitted" {
		t.Fatalf("not admitted: %+v", r)
	}
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 {
		t.Fatalf("queue=%+v", batch)
	}
	if !w.routeBuildPermission(t.Context(), batch[0]) {
		t.Fatal("not routed")
	}
	// The same request queued again after its application.
	w.queuePermission(batch[0])
	for _, req := range w.permissions.queue.Take(false) {
		w.routeBuildPermission(t.Context(), req)
	}
	count := 0
	for _, n := range w.noteList() {
		if strings.HasPrefix(n.Text, "Marshal allowed: /marshal amend") {
			count++
			if strings.Contains(n.Text, "application failed") {
				t.Errorf("a second application was attempted: %s", n.Text)
			}
		}
	}
	if count != 1 {
		t.Fatalf("the decision was recorded %d times, want once", count)
	}
}
