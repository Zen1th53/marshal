//go:build !governed

package tui

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestExtremeMemoryRestoreMenuRequiresExactPreviewAndPreservesUnselected(t *testing.T) {
	ws, r, _ := menuGoalFixture(t, "SESSION-menu-memory", model.ConfirmationApproved)
	source := ws.controlSource()
	local, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(t.Context())
	p, err := r.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	selected := model.MemoryRecordV2{ID: "MEM-menu-selected", ProjectID: p.ID, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryCandidate, Authority: model.AuthorityAgent, Title: "selected", Body: "snapshot body", Scope: "project", ScopeID: p.ID, Source: model.MemorySource{Kind: "fixture", Reference: "original"}, ObservedAt: now, ValidFrom: now, CreatedAt: now}
	unselected := selected
	unselected.ID = "MEM-menu-unselected"
	unselected.Body = "keep this body"
	for _, rec := range []model.MemoryRecordV2{selected, unselected} {
		if err := r.Store().WriteMemoryV2(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	headBytes, err := exec.Command("git", "-C", r.ProjectRoot(), "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))
	snapshot, err := r.CommandCreateMemorySnapshot(ctx, app.CommandEnvelope{ProjectID: source.ProjectID, SessionID: source.SessionID, TargetID: "memory-snapshot:SNAP-menu", IdempotencyKey: "create-menu-snapshot"}, head, []string{selected.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Store().UpdateMemory(ctx, p.ID, selected.ID, 0, func(rec *model.MemoryRecordV2) error { rec.Body = "current body"; return nil }); err != nil {
		t.Fatal(err)
	}
	binding := source.Bindings()["CTUI-0071"]
	if !binding.Bound() {
		t.Fatal("durable restore API remains gapped")
	}
	c := &Confirmation{}
	req := ActionRequest{Action: binding.Action, ProjectID: source.ProjectID, SessionID: source.SessionID, Inputs: map[string]string{"snapshot_id": snapshot.ID, "snapshot_digest": snapshot.Digest, "head": head}}
	if err := c.Begin(t.Context(), binding, req); err != nil {
		t.Fatal(err)
	}
	foreign := c.Request()
	foreign.Target.Digest = strings.Repeat("0", 64)
	if _, err := binding.Execute(t.Context(), foreign); err == nil {
		t.Fatal("foreign preview digest accepted")
	}
	if !strings.Contains(c.Request().Target.Summary, "current body") || !strings.Contains(c.Request().Target.Summary, "snapshot body") {
		t.Fatal("confirmation omitted actual before/after data")
	}
	c.MoveSelection(1)
	if _, err := c.Submit(t.Context()); err == nil {
		t.Fatal("restore omitted destructive acknowledgement")
	}
	c.Acknowledge()
	outcome, err := c.Submit(t.Context())
	if err != nil || outcome.Verdict != VerdictPass {
		t.Fatalf("restore %+v %v", outcome, err)
	}
	replay, err := binding.Execute(t.Context(), c.Request())
	if err != nil || replay.Verdict != VerdictPass {
		t.Fatalf("exact memory receipt failed replay %+v %v", replay, err)
	}
	restored, err := r.Store().GetMemoryV2(ctx, p.ID, selected.ID)
	if err != nil || restored.Body != "snapshot body" || restored.Revision != 2 || restored.Lifecycle != model.MemoryCandidate || restored.Confidence != model.ConfidenceUnverified {
		t.Fatalf("restored candidate %+v %v", restored, err)
	}
	retained, err := r.Store().GetMemoryV2(ctx, p.ID, unselected.ID)
	if err != nil || retained.Body != "keep this body" || retained.Revision != 0 {
		t.Fatalf("unselected memory changed %+v %v", retained, err)
	}
}

func TestExtremeMemoryRestoreMenuAndPaletteOpenTypedSelectors(t *testing.T) {
	for _, entry := range []string{"menu", "palette"} {
		t.Run(entry, func(t *testing.T) {
			ws, _, _ := menuGoalFixture(t, "SESSION-memory-"+entry, model.ConfirmationApproved)
			view, _ := testControlView(t)
			view.OpenAndWait(t.Context())
			view.control.source = ws.controlSource()
			node, _ := view.Nav().ia.Node("CTUI-0071")
			if entry == "palette" {
				view.HandleKey(t.Context(), runeKey('/'))
				for _, r := range node.MenuPath {
					view.HandleKey(t.Context(), runeKey(r))
				}
				view.HandleKey(t.Context(), key(KeyEnter))
			} else if err := view.Nav().DeepLink("CTUI-0071"); err != nil {
				t.Fatal(err)
			}
			view.HandleKey(t.Context(), key(KeyEnter))
			if !view.formOpen() || view.confirmOpen() {
				t.Fatal("selected-record restoration did not open typed selector form")
			}
			view.HandleKey(t.Context(), key(KeyEsc))
			if view.formOpen() {
				t.Fatal("snapshot selector form failed to close")
			}
		})
	}
}

func TestExtremeMemoryRestoreDataCannotEmitTerminalControls(t *testing.T) {
	value := memoryRestoreData("before\x1b[2J\x00\u202e after", 120)
	if strings.ContainsAny(value, "\x1b\x00\u202e") {
		t.Fatalf("terminal controls escaped preview: %q", value)
	}
}
