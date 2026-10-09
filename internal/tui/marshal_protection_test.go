package tui

import (
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/tmux"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/permission"
)

func TestFileProposalProvenanceSurvivesRecovery(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-provenance", false)
	dir := filepath.Join(rt.ProjectRoot(), ".marshal", "proposals")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "request.json"), []byte(`{"action":"setting","key":"control","value":"strict"}`), 0600); err != nil {
		t.Fatal(err)
	}
	w.observeMarshalProposalFiles(rt.ProjectRoot())
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 {
		t.Fatalf("requests: %v", batch)
	}
	text, err := permission.Render(batch)
	if err != nil || !strings.Contains(text, "Who asks: Local request (unverified)") || strings.Contains(text, "The Marshal says") {
		t.Fatalf("popup: %s %v", text, err)
	}
	restarted := NewWorkspace(rt.Store(), rt.ProjectID(), "SESSION-provenance-restart")
	restarted.runtime = rt
	restarted.recoverMarshalProposals()
	recovered := restarted.permissions.queue.Take(false)
	if len(recovered) != 1 || recovered[0].Who != "Local request (unverified)" {
		t.Fatalf("recovered attribution: %v", recovered)
	}
}

func TestBoundRuntimeMarshalProposalAttribution(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-bound-provenance", false)
	tr := importer.SessionTranscript{SessionID: "bound-chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`}}}
	w.observeAuthenticatedMarshalProposals(tr)
	if !w.permissions.queue.Empty() {
		t.Fatal("unbound channel admitted a Marshal request")
	}
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{sessionID: tr.SessionID}
	w.observeAuthenticatedMarshalProposals(tr)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Who != "Marshal" {
		t.Fatalf("bound attribution: %v", batch)
	}
}

func TestLocalIntakeUpdateIsVisible(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-intake-visibility", false)
	emitMarshalIntakeFile(t, w, "English", "no")
	if !strings.Contains(w.state.LastOutput, "Local request (unverified): intake updated") {
		t.Fatalf("activity: %s", w.state.LastOutput)
	}
}

func TestReservedGovernancePopupDLeavesResultUnmerged(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-reserved-deny", false)
	service := rt.Marshal()
	run := marshal.Run{PlanID: "PLAN-reserved", PlanVersion: 2, Settings: marshal.DefaultSettings(), State: marshal.Reviewing, Tasks: []marshal.Task{{PlanTaskID: "a", Mode: marshal.Governed, State: marshal.Accepted, ResultCommit: strings.Repeat("a", 40)}}}
	revision, err := rt.Store().SetMarshalRun(t.Context(), rt.ProjectID(), "reserved-run", run, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	m.service, m.runID = service, "reserved-run"
	w.queueReservedMergeApproval(t.Context(), "reserved-run", "a", revision, run.Tasks[0].ResultCommit, []string{"AGENTS.md"})
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 {
		t.Fatalf("requests: %v", batch)
	}
	text, err := permission.Render(batch)
	if err != nil || !strings.Contains(text, "AGENTS.md") || !strings.Contains(text, "A = Allow   D = Deny") {
		t.Fatalf("popup: %s %v", text, err)
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	// Execute the production popup program with the operator's D key.
	script := "#!/bin/bash\nif [[ $* == *'#{client_height} #{client_width}'* ]]; then echo '40 180'; exit; fi\nif [[ $1 == display-popup ]]; then printf D | bash -c \"${@: -1}\"; exit; fi\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	allow, err := permission.Popup(t.Context(), "client", batch, time.Second)
	if err != nil || allow {
		t.Fatalf("D: allow=%v err=%v", allow, err)
	}
	m.grant("reserved-run", batch[0].TaskID) // D also withdraws an earlier unspent consent.
	if err := w.decidePermission(t.Context(), batch[0], allow, "operator popup"); err != nil {
		t.Fatal(err)
	}
	after, err := rt.Store().GetMarshalRun(t.Context(), rt.ProjectID(), "reserved-run")
	if err != nil || after.Revision != revision || after.Value.Tasks[0].State != marshal.Accepted {
		t.Fatalf("denial changed state: %+v %v", after, err)
	}
	if _, err := m.approver(t.Context(), "reserved-run", batch[0].TaskID); err == nil {
		t.Fatal("denial granted merge authority")
	}
	m.busy = true
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err == nil {
		t.Fatal("busy resume accepted consent")
	}
	m.busy = false
	if _, err := m.approver(t.Context(), "reserved-run", batch[0].TaskID); err == nil {
		t.Fatal("failed resume left usable consent")
	}
	// An old result binding also cannot grant consent after a revision change.
	if _, err := rt.Store().SetMarshalRun(t.Context(), rt.ProjectID(), "reserved-run", run, revision); err != nil {
		t.Fatal(err)
	}
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("stale approval: %v", err)
	}
}

func TestMonitorShowsGoverningIntegrityAlert(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-integrity-alert", false)
	service := rt.Marshal()
	run := marshal.Run{PlanID: "PLAN-integrity", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Approved, GoverningDigest: "sha256:original"}
	if _, err := rt.Store().SetMarshalRun(t.Context(), rt.ProjectID(), "integrity-run", run, 0); err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	m.service, m.runID = service, "integrity-run"
	w.observeMarshalProposalFiles(rt.ProjectRoot())
	if !strings.Contains(w.state.LastOutput, "Governance integrity alert: governing files changed unexpectedly") {
		t.Fatalf("activity: %s", w.state.LastOutput)
	}
	w.observeMarshalProposalFiles(rt.ProjectRoot())
	events, err := rt.Store().MarshalDecisions(t.Context(), "integrity-run")
	if err != nil || len(events) != 1 {
		t.Fatalf("duplicate/missing alert: %v %v", events, err)
	}
}

func TestMonitorSkipsMissingRunAndAlertsOnLaterMismatch(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-missing-run-integrity", false)
	service := rt.Marshal()
	runID := "pending-start-run"
	m := w.marshalSession()
	m.service, m.runID = service, runID

	// 1. Missing run record must not produce a governance integrity alert.
	w.observeMarshalProposalFiles(rt.ProjectRoot())
	if strings.Contains(w.state.LastOutput, "Governance integrity alert") {
		t.Fatalf("unexpected alert on unpersisted run: %s", w.state.LastOutput)
	}
	m.mu.Lock()
	alerted := m.integrityAlertRunID
	m.mu.Unlock()
	if alerted != "" {
		t.Fatalf("expected integrityAlertRunID to remain empty for unpersisted run, got %q", alerted)
	}

	// 2. Once the run is saved with a mismatched baseline, integrity monitoring must alert.
	run := marshal.Run{
		PlanID:          "PLAN-integrity-late",
		PlanVersion:     1,
		Settings:        marshal.DefaultSettings(),
		State:           marshal.Approved,
		GoverningDigest: "sha256:mismatch",
	}
	if _, err := rt.Store().SetMarshalRun(t.Context(), rt.ProjectID(), runID, run, 0); err != nil {
		t.Fatal(err)
	}

	w.observeMarshalProposalFiles(rt.ProjectRoot())
	if !strings.Contains(w.state.LastOutput, "Governance integrity alert: governing files changed unexpectedly") {
		t.Fatalf("expected alert on later mismatch, got activity: %s", w.state.LastOutput)
	}
	m.mu.Lock()
	alerted = m.integrityAlertRunID
	m.mu.Unlock()
	if alerted != runID {
		t.Fatalf("expected integrityAlertRunID = %q, got %q", runID, alerted)
	}

	// 3. Repeated check should not emit duplicate alerts.
	w.observeMarshalProposalFiles(rt.ProjectRoot())
	events, err := rt.Store().MarshalDecisions(t.Context(), runID)
	if err != nil || len(events) != 1 {
		t.Fatalf("duplicate/missing alert: %v %v", events, err)
	}
}

func TestLegacyProposalAttributionDowngradeStillResolvesDuplicates(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-legacy-source", false)
	line := `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`
	req := permission.Request{Kind: "marshal-command", Object: "/marshal settings control strict", Who: "Marshal", Scope: "this project, next Marshal run"}
	for _, id := range []string{"legacy-one", "legacy-two"} {
		req.ProposalID = id
		if _, err := rt.Store().AdmitMarshalProposal(t.Context(), rt.ProjectID(), id, line); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(req)
		if err := rt.Store().BindMarshalProposal(t.Context(), rt.ProjectID(), id, string(data), req.Key()); err != nil {
			t.Fatal(err)
		}
	}
	w.recoverMarshalProposals()
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Who != "Local request (unverified)" {
		t.Fatalf("legacy attribution: %v", batch)
	}
	if err := w.decidePermission(t.Context(), batch[0], false, "operator popup"); err != nil {
		t.Fatal(err)
	}
	pending, err := rt.Store().PendingMarshalProposals(t.Context(), rt.ProjectID())
	if err != nil || len(pending) != 0 {
		t.Fatalf("resolved legacy requests replay: %v %v", pending, err)
	}
}

func TestLocalProposalKeepsHumanPopupDeadline(t *testing.T) {
	req := permission.Request{ProposalID: "local-proposal", Kind: "read", Who: "Local request (unverified)"}
	if got := permissionPopupTimeout([]permission.Request{req}); got != 120*time.Second {
		t.Fatalf("local proposal review deadline = %s", got)
	}
}

func TestImportedMemoryCandidateRequestIsUnverified(t *testing.T) {
	req := memoryPermission(model.MemoryRecordV2{ID: "MEM-local", SessionID: "imported", Body: "candidate"})
	if req.Who != "Local request (unverified)" {
		t.Fatalf("candidate attribution: %s", req.Who)
	}
	if got := permissionPopupTimeout([]permission.Request{req}); got != 120*time.Second {
		t.Fatalf("candidate review deadline: %s", got)
	}
}

func TestLocalMemoryProposalDeduplicationResolvesRetainedOccurrence(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-memory-source", false)
	req := memoryPermission(model.MemoryRecordV2{ID: "MEM-candidate", Body: "candidate"})
	w.queuePermission(req) // The automatic candidate request precedes the proposal.
	req.ProposalID = "local-memory-proposal"
	if _, err := rt.Store().AdmitMarshalProposal(t.Context(), rt.ProjectID(), req.ProposalID, `MARSHAL_PROPOSAL {"action":"memory","id":"MEM-candidate"}`); err != nil {
		t.Fatal(err)
	}
	w.queuePermission(req)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Who != "Local request (unverified)" {
		t.Fatalf("duplicate memory request: %v", batch)
	}
	if err := w.decidePermission(t.Context(), batch[0], false, "operator popup"); err != nil {
		t.Fatal(err)
	}
	pending, err := rt.Store().PendingMarshalProposals(t.Context(), rt.ProjectID())
	if err != nil || len(pending) != 0 {
		t.Fatalf("deduplicated proposal remains pending: %v %v", pending, err)
	}
}
