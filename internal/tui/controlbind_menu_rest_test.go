package tui

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/protocol"
)

// submitMenuForm opens spec through the advanced menu, types each field and
// confirms, exactly as an operator does.
func submitMenuForm(t *testing.T, ws *Workspace, spec string, fields ...string) Outcome {
	t.Helper()
	ctx := context.Background()
	if ws.navView.Confirmation().Phase() != PhaseIdle {
		ws.navView.Confirmation().Dismiss()
	}
	if err := ws.navView.Nav().DeepLink(spec); err != nil {
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	for _, text := range fields {
		for _, r := range text {
			ws.dispatchNavigationKey(ctx, runeKey(r))
		}
		ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyRight})
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	return ws.navView.Confirmation().Outcome()
}

func menuRestImportTask(t *testing.T, runtime *app.Runtime, id string) {
	t.Helper()
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{ID: id, Title: "menu " + id, Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
}

func TestMenuRestBindingsAreLiveOverTheRuntime(t *testing.T) {
	ws, _ := realControlWorkspace(t, "SESSION-menu-rest-bound")
	bindings := ws.controlSource().Bindings()
	for _, id := range []ActionID{"CTUI-0649", "CTUI-0320", "CTUI-0321", "CTUI-0318", "CTUI-0248"} {
		b := bindings[id]
		if !b.Bound() || b.Requires != "" || len(b.Inputs) == 0 || b.PrepareRequest == nil {
			t.Fatalf("%s is not a live form: %+v", id, b)
		}
		if w := Wired(string(id)); w.UnderConstruction() {
			t.Fatalf("%s is still under construction in wired.json", id)
		}
	}
	unbound := (&ControlSource{Authority: newFakeAuthority()}).Bindings()
	if unbound["CTUI-0320"].Bound() || unbound["CTUI-0320"].Requires == "" {
		t.Fatal("an authority without the operator boundary must not get handoff power")
	}
}

func TestMenuPolicySuiteRunsThroughConfirmation(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-policy")
	p := policy.Policy{ID: "menu-policy", Version: 1, Default: policy.Effect("deny")}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite, _ := json.Marshal(map[string]any{"id": "menu-suite", "cases": []any{map[string]any{
		"id": "case-1", "name": "default decision",
		"given": map[string]any{
			"policy":  map[string]any{"id": string(p.ID), "version": p.Version, "default": string(p.Default), "rules": []any{}},
			"binding": map[string]any{"version": p.Version, "digest": string(digest), "generation": 1},
		},
		"when":   map[string]any{"subject_id": "s", "task_id": "t", "change_id": "c", "action": "read", "resource": "repo"},
		"expect": map[string]any{"decision": "deny"},
	}}})
	if err := os.WriteFile(filepath.Join(runtime.ProjectRoot(), "menu-suite.json"), suite, 0o600); err != nil {
		t.Fatal(err)
	}
	outcome := submitMenuForm(t, ws, "CTUI-0649", "menu-suite.json")
	if !outcome.Succeeded() || !strings.Contains(outcome.Detail, "1 cases, 0 not passing") {
		t.Fatalf("policy suite: %+v", outcome)
	}
}

func TestMenuTypedAndMemoryHandoffsReadBackAccepted(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-handoff")
	menuRestImportTask(t, runtime, "TASK-MENU-HANDOFF")
	typed := submitMenuForm(t, ws, "CTUI-0320", "TASK-MENU-HANDOFF", "qa", "ready for review")
	if !typed.Succeeded() || !strings.Contains(typed.Detail, "to qa is accepted") {
		t.Fatalf("typed handoff: %+v", typed)
	}
	memory := submitMenuForm(t, ws, "CTUI-0321", "TASK-MENU-HANDOFF", "developer")
	if !memory.Succeeded() || !strings.Contains(memory.Detail, "to developer") {
		t.Fatalf("memory handoff: %+v", memory)
	}
	refused := submitMenuForm(t, ws, "CTUI-0320", "TASK-NOWHERE", "qa", "x")
	if refused.Succeeded() {
		t.Fatalf("a handoff for a task that does not exist succeeded: %+v", refused)
	}
	if _, err := runtime.Store().GetHandoff(context.Background(), protocol.HandoffID("TASK-NOWHERE")); err == nil {
		t.Fatal("unexpected handoff row")
	}
}

func TestMenuClaimChallengeContestsWithStoredEvidence(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-claim")
	ctx := context.Background()
	now := time.Now().UTC()
	claim := model.Claim{ID: "CLAIM-menu", GoalID: "GOAL-menu", GoalRevision: 1, Subject: "tests", NormalizedText: "all tests pass", Scope: "repo",
		Criticality: model.CriticalityStandard, State: model.ClaimStateSupported, Author: model.AuthorProvenance{AgentID: "AGENT-dev"}, CreatedAt: now, UpdatedAt: now}
	if err := runtime.Store().SaveClaim(ctx, claim); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{"task_id": "TASK-menu"}
	digest, err := evidence.CanonicalDigest(evidence.NodeTypeClaim, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Store().PutNode(ctx, evidence.Node{ID: "EVIDENCE-MENU", Type: evidence.NodeTypeClaim, Digest: digest, Metadata: metadata, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	invented := submitMenuForm(t, ws, "CTUI-0318", "CLAIM-menu", "EVIDENCE-INVENTED", "made up")
	if invented.Succeeded() {
		t.Fatalf("invented counter-evidence was accepted: %+v", invented)
	}
	outcome := submitMenuForm(t, ws, "CTUI-0318", "CLAIM-menu", "EVIDENCE-MENU", "the failing run contradicts it")
	if !outcome.Succeeded() || !strings.Contains(outcome.Detail, "CONTESTED") {
		t.Fatalf("challenge: %+v", outcome)
	}
}

func TestMenuInterpretationCorrectionRevisesTheGoal(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-correct")
	// With no goal the form does not open: the refusal comes first.
	if b := ws.controlSource().Bindings()["CTUI-0248"]; b.Prepare == nil {
		t.Fatal("CTUI-0248 has no pre-form check")
	} else if _, err := b.Prepare(context.Background()); err == nil {
		t.Fatal("CTUI-0248 pre-form check passed with no goal")
	}
	now := time.Now().UTC()
	goal := model.GoalContract{ID: "GOAL-menu-correct", SessionID: ws.sessionID, ProjectID: runtime.ProjectIdentity(),
		OriginalRequest: "fix the typo", RequestDigest: "sha256:menu-correct", ConstitutionVersion: constitution.Current.String(),
		Confirmation: model.ConfirmationPending, Revision: 1, DesiredOutcome: "the typo is fixed", Risk: model.R1, AuthoritySource: "fixture",
		UnresolvedDecisions: []model.UnresolvedDecision{{ID: "DEC-file", Question: "Which file?", Options: []string{"README.md", "docs/index.md"}, RequiresUser: true}},
		CreatedAt:           now, UpdatedAt: now}
	if err := runtime.Store().SaveGoalContract(context.Background(), goal, 0); err != nil {
		t.Fatal(err)
	}
	wrong := submitMenuForm(t, ws, "CTUI-0248", "DEC-nope", "README.md")
	if wrong.Succeeded() {
		t.Fatalf("an unknown decision was resolved: %+v", wrong)
	}
	outcome := submitMenuForm(t, ws, "CTUI-0248", "DEC-file", "README.md")
	if !outcome.Succeeded() || outcome.Target.Revision != 2 {
		t.Fatalf("correction: %+v", outcome)
	}
	after, err := runtime.Store().GetActiveGoalContract(context.Background(), ws.sessionID)
	if err != nil || len(after.UnresolvedDecisions) != 0 || after.Confirmation != model.ConfirmationPending {
		t.Fatalf("goal after correction: %+v %v", after, err)
	}
}

func TestMenuStartsLocalServersOnTheWorkspaceRuntime(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-servers")
	ctx := context.Background()
	start := func(spec string) Outcome {
		if ws.navView.Confirmation().Phase() != PhaseIdle {
			ws.navView.Confirmation().Dismiss()
		}
		if err := ws.navView.Nav().DeepLink(spec); err != nil {
			t.Fatal(err)
		}
		ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
		if spec != "CTUI-0705" {
			for range 32 {
				ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyBackspace})
			}
			for _, r := range "127.0.0.1:0" {
				ws.dispatchNavigationKey(ctx, runeKey(r))
			}
			ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
		}
		ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyRight})
		ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
		return ws.navView.Confirmation().Outcome()
	}
	for _, spec := range []string{"CTUI-0741", "CTUI-0750", "CTUI-0705"} {
		outcome := start(spec)
		if !outcome.Succeeded() || !strings.Contains(outcome.Detail, "listening on") {
			t.Fatalf("%s: %+v", spec, outcome)
		}
	}
	if again := start("CTUI-0741"); again.Succeeded() {
		t.Fatalf("a second MCP server started: %+v", again)
	}
	status, ok := ws.controlSource().Authority.(LocalServerAuthority).LocalServerStatus("mcp")
	if !ok {
		t.Fatal("MCP server is not running")
	}
	ws.closeLocalServers()
	if _, ok := ws.controlSource().Authority.(LocalServerAuthority).LocalServerStatus("mcp"); ok {
		t.Fatal("servers survived the workspace closing them")
	}
	if conn, err := net.Dial("tcp", status.Address); err == nil {
		conn.Close()
		t.Fatal("MCP listener still accepts connections")
	}
	_ = runtime
}

func TestMenuRestUnderConstructionCausesMatchWired(t *testing.T) {
	bindings := (&ControlSource{Authority: newFakeAuthority()}).Bindings()
	for _, entry := range menuRestUnderConstruction {
		w := Wired(entry.id)
		b := bindings[ActionID(entry.id)]
		// The walk records the refusal as the menu displays it, so the
		// cause is the requirement after communityLabel ("Process 08" reads
		// "optimization service").
		cause := w.Cause == entry.requires || w.Cause == communityLabel(entry.requires)
		if !w.UnderConstruction() || !cause || b.Bound() || b.Requires != entry.requires || w.Task == "" {
			t.Fatalf("%s: wired %+v binding %+v", entry.id, w, b)
		}
	}
}

// A waiver expiry that is not a time is refused in words, not with Go's
// reference layout (e2e 2026-11-06, Authorized finding closure).
func TestWaiverExpiryRefusalIsReadable(t *testing.T) {
	err := validateWaiverExpiry("zz/q?bad id")
	if err == nil || strings.Contains(err.Error(), "2006-01-02T15:04:05Z07:00") || !strings.Contains(err.Error(), "RFC 3339") {
		t.Fatalf("got %v", err)
	}
	if err := validateWaiverExpiry("2026-11-06T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
}
