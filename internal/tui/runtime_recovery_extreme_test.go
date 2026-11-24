//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func runtimeRecoveryFixture(t *testing.T) (*Workspace, *proposalMCPLease) {
	t.Helper()
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorkspace(r.Store(), r.ProjectID(), "runtime-regression")
	w.runtime = r
	w.workDir = r.ProjectRoot()
	l := &proposalMCPLease{id: "launch", owner: "owner", provider: "codex", active: true, dir: &briefingDir{}}
	w.proposalMCP.active = l
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {provider: "codex", role: "marshal-chat", state: "working", briefingDir: l.dir}}
	t.Cleanup(func() { w.Close(); _ = r.Close() })
	if err := w.store.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	return w, l
}

func TestRuntimeRecoveryRefusalLoggedOnce(t *testing.T) {
	w, l := runtimeRecoveryFixture(t)
	c := projectcontext.Contract{FormatVersion: 1, Project: w.runtime.ProjectContractBinding(), ID: "architecture", Revision: 1, Purpose: "Cover the project", Components: []projectcontext.Component{{ID: "core", Responsibility: "Core", Paths: []string{"src"}}}, Invariants: []string{"Keep all code covered"}}
	c.ReviewedInventoryDigest = strings.Repeat("a", 64)
	p, _, err := w.store.AdmitMCPProjectContract(t.Context(), store.MCPProposal{ID: "mcp:regression", Launch: l.id, Owner: l.owner, Provider: l.provider, Submission: "draft"}, store.ProjectContractSubmission{Action: "project-contract", Contract: c})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.workDir, "uncovered.go"), []byte("package uncovered"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := w.store.PendingMarshalProposals(t.Context(), w.projectID)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err, p)
	}
	for range 10 {
		if !w.recoverBoundMCPMemory(rows[0]) {
			t.Fatal("not handled")
		}
	}
	if n := strings.Count(w.state.LastOutput, "Live MCP project contract recovery refused"); n != 1 {
		t.Fatalf("refusal logged %d times", n)
	}
	if !w.permissions.queue.Empty() {
		t.Fatal("invalid draft was queued")
	}
	if err := os.Remove(filepath.Join(w.workDir, "uncovered.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.resumeProjectContractMCP(t.Context(), l, p); err != nil {
		t.Fatal(err)
	}
	if w.permissions.queue.Empty() || l.recoveryRefusals[p.ID] {
		t.Fatal("corrected explicit retry did not recover")
	}

}

func TestRuntimeDesignReviewBrokerPollsOutsideRequest(t *testing.T) {
	w, l := runtimeRecoveryFixture(t)
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("MARSHAL_TEST_CALLS", calls)
	script := `#!/bin/sh
printf 'launch\n' >> "$MARSHAL_TEST_CALLS"
sleep 0.1
printf '%s\n' '{"type":"result","session_id":"fresh-review","structured_output":{"findings":"Review completed."}}'
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	raw := json.RawMessage(`{"provider":"claude","draft":"Review this design","reading_list":["README.md"]}`)
	requestCtx, cancel := context.WithCancel(t.Context())
	out, err := w.handleProposalMCP(requestCtx, l, "design_review", raw)
	cancel()
	if err != nil || !strings.Contains(string(out), `"state":"pending"`) {
		t.Fatal(string(out), err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err = w.handleProposalMCP(t.Context(), l, "design_review", raw)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), `"state":"completed"`) {
			break
		}
		if strings.Contains(string(out), `"state":"failed"`) || time.Now().After(deadline) {
			t.Fatal(string(out))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(string(out), "fresh-review") {
		t.Fatal(string(out))
	}
	data, err := os.ReadFile(calls)
	if err != nil || string(data) != "launch\n" {
		t.Fatalf("poll relaunched provider: %q %v", data, err)
	}
}

func TestRuntimeDesignReviewLeaseRevocationStopsReviewer(t *testing.T) {
	w, l := runtimeRecoveryFixture(t)
	l.cleanup = func() {}
	bin := t.TempDir()
	started := filepath.Join(t.TempDir(), "started")
	t.Setenv("MARSHAL_TEST_REVIEW_STARTED", started)
	script := "#!/bin/sh\ntouch \"$MARSHAL_TEST_REVIEW_STARTED\"\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if _, err := w.handleProposalMCP(t.Context(), l, "design_review", json.RawMessage(`{"provider":"claude","draft":"Review design"}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reviewer did not launch")
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.revokeProposalLease(l)
	for {
		var state string
		for _, job := range l.designReviews {
			job.mu.Lock()
			state = job.state
			job.mu.Unlock()
		}
		if state == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("revocation did not stop reviewer")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDesignReviewRequiredEndToEndThroughBroker(t *testing.T) {
	ctx := t.Context()
	w, l := runtimeRecoveryFixture(t)

	bin := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("MARSHAL_TEST_REVIEW_ARGS", argsFile)
	script := `#!/bin/sh
printf '%s\n' "$@" > "$MARSHAL_TEST_REVIEW_ARGS"
printf '%s\n' '{"type":"result","session_id":"fresh-e2e-session","structured_output":{"findings":"Check failure handling on write."}}'
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	// 1. Configure design-review=required in project settings and draft a run.
	rec, err := w.store.GetMarshalSettings(ctx, w.projectID)
	if err != nil {
		t.Fatal(err)
	}
	reqSettings, err := settingscatalog.Apply(rec.Value, "design-review", "required")
	if err != nil {
		t.Fatalf("Apply design-review=required: %v", err)
	}
	if _, _, err := w.store.CommitMarshalSettings(ctx, w.projectID, reqSettings, rec.Revision, "owner"); err != nil {
		t.Fatal(err)
	}

	service := w.runtime.Marshal()
	service.ApprovalActor = func(context.Context, string, string) (string, error) { return "user", nil }
	makeDraft := func(planID, file, instructions string) app.MarshalDraft {
		pt := plan.Task{
			ID:           "t1",
			Title:        "write " + file,
			Criteria:     []string{file + " exists"},
			Paths:        []string{file},
			Instructions: instructions,
			Mutating:     true,
			Weight:       1,
		}
		graph, err := plan.BuildGraph([]plan.Task{pt})
		if err != nil {
			t.Fatal(err)
		}
		return app.MarshalDraft{
			Plan: plan.ExecutionPlan{
				ID:        planID,
				ProjectID: projectid.ID(w.runtime.ProjectIdentity()),
				Goal:      plan.GoalBinding{GoalID: "GOAL-" + planID, Revision: 1},
				Version:   1,
				State:     plan.StateReady,
				Mode:      plan.ModeStandard,
				Tasks:     []plan.Task{pt},
				Checks:    map[string][]string{"t1": {"test -f " + file}},
				Graph:     graph,
			},
			Tasks: []marshal.Task{{
				PlanTaskID:   "t1",
				Title:        "write " + file,
				Worker:       "worker",
				Mode:         marshal.Native,
				Files:        []string{file},
				Criteria:     []string{file + " exists"},
				Checks:       []marshal.Check{{Command: "test -f " + file, Criteria: []string{file + " exists"}}},
				Instructions: instructions,
			}},
		}
	}

	draft1 := makeDraft("PLAN-e2e-1", "a.txt", "write a.txt safely")
	if _, err := service.StartPlanningFromDraft(ctx, "run-e2e-1", "write a.txt", draft1, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.runID = "run-e2e-1"
	m.service = service
	m.mu.Unlock()

	// Approval without design review must be refused.
	if _, err := service.Approve(ctx, "run-e2e-1"); err == nil || !strings.Contains(err.Error(), "design review receipt") {
		t.Fatalf("expected approval refusal before design review, got %v", err)
	}

	// 2. Flip LIVE project setting to design-review=off; ReviewDesign must still
	// gate on the run's policy snapshot (required), not the live project setting.
	rec2, err := w.store.GetMarshalSettings(ctx, w.projectID)
	if err != nil {
		t.Fatal(err)
	}
	offSettings, err := settingscatalog.Apply(rec2.Value, "design-review", "off")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.store.CommitMarshalSettings(ctx, w.projectID, offSettings, rec2.Revision, "owner"); err != nil {
		t.Fatal(err)
	}

	// 3. Run design review through the real MCP broker path (without supplying run_id).
	wantDigest, err := service.RunDesignDraftDigest(ctx, "run-e2e-1")
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"provider":"claude","draft":"Short caller draft note","reading_list":["README.md"]}`)
	deadline := time.Now().Add(10 * time.Second)
	var brokerResp struct {
		State  string                 `json:"state"`
		Result app.DesignReviewResult `json:"result"`
		Reason string                 `json:"reason"`
	}
	for {
		out, err := w.handleProposalMCP(ctx, l, "design_review", raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out, &brokerResp); err != nil {
			t.Fatal(err)
		}
		if brokerResp.State == "completed" {
			break
		}
		if brokerResp.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("design review did not complete: %+v", brokerResp)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if brokerResp.Result.RunID != "run-e2e-1" || brokerResp.Result.DraftDigest != wantDigest {
		t.Fatalf("broker result mismatch: %+v (want run=run-e2e-1 digest=%s)", brokerResp.Result, wantDigest)
	}
	if brokerResp.Result.DraftDigest == marshal.DesignDraftDigest("Short caller draft note") {
		t.Fatal("broker stamped caller-supplied text digest instead of RunDesignDraftDigest")
	}
	argsText, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(argsText), "write a.txt safely") || !strings.Contains(string(argsText), wantDigest) {
		t.Fatalf("reviewer prompt missing canonical run draft details: %s", argsText)
	}

	// 4. Approval now succeeds with the attached receipt.
	approved, err := service.Approve(ctx, "run-e2e-1")
	if err != nil {
		t.Fatalf("Approve after broker design review: %v", err)
	}
	if approved.DesignReview == nil || approved.DesignReview.DraftDigest != wantDigest || approved.DesignReview.SessionID != "fresh-e2e-session" {
		t.Fatalf("approved run missing attached design review receipt: %+v", approved.DesignReview)
	}

	// 5. A different draft's receipt must be refused.
	rec3, err := w.store.GetMarshalSettings(ctx, w.projectID)
	if err != nil {
		t.Fatal(err)
	}
	reqAgain, err := settingscatalog.Apply(rec3.Value, "design-review", "required")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.store.CommitMarshalSettings(ctx, w.projectID, reqAgain, rec3.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	draft2 := makeDraft("PLAN-e2e-2", "b.txt", "write b.txt differently")
	if _, err := service.StartPlanningFromDraft(ctx, "run-e2e-2", "write b.txt", draft2, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordDesignReview(ctx, "run-e2e-2", brokerResp.Result.Receipt()); err == nil {
		t.Fatal("different draft's design review receipt was accepted")
	}
	if _, err := service.Approve(ctx, "run-e2e-2"); err == nil || !strings.Contains(err.Error(), "design review receipt") {
		t.Fatalf("different draft approved without its own design review: %v", err)
	}

	// 6. A drafting run whose snapshot has design-review=off must refuse review
	// even if live project settings are later changed to on-demand.
	rec4, err := w.store.GetMarshalSettings(ctx, w.projectID)
	if err != nil {
		t.Fatal(err)
	}
	offAgain, err := settingscatalog.Apply(rec4.Value, "design-review", "off")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.store.CommitMarshalSettings(ctx, w.projectID, offAgain, rec4.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	draft3 := makeDraft("PLAN-e2e-3", "c.txt", "write c.txt")
	if _, err := service.StartPlanningFromDraft(ctx, "run-e2e-3", "write c.txt", draft3, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.runID = "run-e2e-3"
	m.mu.Unlock()
	rec5, err := w.store.GetMarshalSettings(ctx, w.projectID)
	if err != nil {
		t.Fatal(err)
	}
	onDemand, err := settingscatalog.Apply(rec5.Value, "design-review", "on-demand")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.store.CommitMarshalSettings(ctx, w.projectID, onDemand, rec5.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	offRaw := json.RawMessage(`{"provider":"claude","draft":"Review draft 3","reading_list":["README.md"]}`)
	deadline = time.Now().Add(10 * time.Second)
	for {
		out, err := w.handleProposalMCP(ctx, l, "design_review", offRaw)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out, &brokerResp); err != nil {
			t.Fatal(err)
		}
		if brokerResp.State == "failed" {
			break
		}
		if brokerResp.State == "completed" || time.Now().After(deadline) {
			t.Fatalf("expected run snapshot design-review=off refusal, got %+v", brokerResp)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(brokerResp.Reason, "disabled by run review policy") {
		t.Fatalf("unexpected refusal reason: %+v", brokerResp)
	}
}
