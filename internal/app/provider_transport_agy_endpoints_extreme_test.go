//go:build !governed && linux

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

func agyTransportReviewFixture(t *testing.T) (*Runtime, providerTransportBinding, model.Task, string) {
	t.Helper()
	r, task, pkg, canonical, agent := transportFixtureForProvider(t, false, "agy")
	_, pause, e := r.reviewProviderTransport(t.Context(), task, pkg, canonical, agent, "agy", "gemini-3.8-flash-high", task.WorktreePath)
	if e != nil || pause == nil || pause.ApprovalReq == nil {
		t.Fatalf("canonical AGY fixture review failed: %v", e)
	}
	var b providerTransportBinding
	if json.Unmarshal([]byte(pause.ApprovalReq.Parameters), &b) != nil {
		t.Fatal("binding invalid")
	}
	return r, b, canonical, agent
}
func agyTransportBindingWithExtra(t *testing.T, b providerTransportBinding, extras []string) providerTransportBinding {
	t.Helper()
	raw, _ := json.Marshal(b)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["AdditionalEndpoints"] = extras
	raw, _ = json.Marshal(m)
	var out providerTransportBinding
	if json.Unmarshal(raw, &out) != nil {
		t.Fatal("fixture decode failed")
	}
	return out
}
func TestAGYHostTransportProposalHasNoEndpointAuthority(t *testing.T) {
	_, b, _, _ := agyTransportReviewFixture(t)
	req := transportApprovalRequest(b)
	var raw map[string]any
	if json.Unmarshal([]byte(req.Parameters), &raw) != nil {
		t.Fatal("invalid visible binding")
	}
	if _, ok := raw["AdditionalEndpoints"]; ok || b.Endpoint != "" || req.TargetResource != "agy" || strings.Contains(req.Scope, "www.googleapis.com:443") {
		t.Fatal("host transport proposal retained endpoint authority")
	}
	old := agyTransportBindingWithExtra(t, b, []string{"www.googleapis.com:443"})
	oldReq := transportApprovalRequest(old)
	if execution.ComputeActionDigest(req.OperationType, req.TargetResource, req.DiffPreview, req.Parameters) == execution.ComputeActionDigest(oldReq.OperationType, oldReq.TargetResource, oldReq.DiffPreview, oldReq.Parameters) || execution.ComputeStateDigest(req.CurrentState) == execution.ComputeStateDigest(oldReq.CurrentState) {
		t.Fatal("endpoint-bearing approval has same canonical action/state")
	}
}
func TestAGYHostTransportRejectsEndpointTampering(t *testing.T) {
	r, b, c, agent := agyTransportReviewFixture(t)
	for _, tc := range []struct {
		name    string
		extras  []string
		primary string
		pass    bool
	}{
		{"exact", nil, "", true},
		{"old-primary-only", nil, "daily-cloudcode-pa.googleapis.com:443", false},
		{"duplicate", []string{"www.googleapis.com:443", "www.googleapis.com:443"}, "daily-cloudcode-pa.googleapis.com:443", false},
		{"primary-as-extra", []string{"daily-cloudcode-pa.googleapis.com:443"}, "daily-cloudcode-pa.googleapis.com:443", false},
		{"foreign", []string{"accounts.google.com:443"}, "daily-cloudcode-pa.googleapis.com:443", false},
		{"uppercase", []string{"WWW.GOOGLEAPIS.COM:443"}, "daily-cloudcode-pa.googleapis.com:443", false},
		{"space", []string{" www.googleapis.com:443"}, "daily-cloudcode-pa.googleapis.com:443", false},
		{"reversed", []string{"daily-cloudcode-pa.googleapis.com:443"}, "www.googleapis.com:443", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := agyTransportBindingWithExtra(t, b, tc.extras)
			value.Endpoint = tc.primary
			req := transportApprovalRequest(value)
			auth := providerTransportAuthorization{binding: value, action: execution.ComputeActionDigest(req.OperationType, req.TargetResource, req.DiffPreview, req.Parameters), state: execution.ComputeStateDigest(req.CurrentState)}
			ctx := context.WithValue(t.Context(), providerTransportContextKey{}, auth)
			handled, e := r.authorizeReviewedProviderTransport(ctx, "fixture-egress", b.Run, "agy", b.Model, c, agent, b.Run, model.RoleDeveloper, "")
			if tc.pass {
				if !handled || e != nil {
					t.Fatalf("exact set refused: %v", e)
				}
				return
			}
			if !handled || !errors.Is(e, execution.ErrApprovalTOCTOUViolation) {
				t.Fatal("changed endpoint authority accepted before canonical admission")
			}
			if e := r.consumeReviewedProviderTransport(ctx, tc.primary, "fixture-egress"); !errors.Is(e, execution.ErrApprovalTOCTOUViolation) {
				t.Fatal("changed endpoint set reached credential/approval consumption")
			}
		})
	}
}

func TestAGYTransportRequestContextAndPromptRejectEndpointInjection(t *testing.T) {
	r, b, c, agent := agyTransportReviewFixture(t)
	req := transportApprovalRequest(b)
	published := withProviderTransportAuthorization(t.Context(), providerTransportAuthorization{binding: b, prompt: &providerPromptAdmission{runtime: r, binding: b}})
	got := published.Value(providerTransportContextKey{}).(providerTransportAuthorization)
	if got.binding.Endpoint != "" || got.prompt.binding.Endpoint != "" || strings.Contains(req.Parameters, "googleapis.com") {
		t.Fatal("host approval captured endpoint authority")
	}
	got.binding.AdditionalEndpoints = []string{"oauth2.googleapis.com:443"}
	if len(got.prompt.binding.AdditionalEndpoints) != 0 {
		t.Fatal("prompt and authorization share endpoint mutation")
	}
	handled, e := r.authorizeReviewedProviderTransport(published, "fixture-egress", b.Run, "agy", b.Model, c, agent, b.Run, model.RoleDeveloper, "daily-cloudcode-pa.googleapis.com:443")
	if !handled || !errors.Is(e, execution.ErrApprovalTOCTOUViolation) {
		t.Fatal("postpublication endpoint tamper was not refused")
	}
}

func TestAGYOldPrimaryOnlyTransportApprovalPersistsAndRefusesAfterReopen(t *testing.T) {
	r, b, c, agent := agyTransportReviewFixture(t)
	ctx := t.Context()
	old := cloneProviderTransportBinding(b)
	old.Endpoint = "daily-cloudcode-pa.googleapis.com:443"
	req := transportApprovalRequest(old)
	req.RunID = b.Run
	req.TaskID = b.Task
	req.PlanID = b.Plan
	req.PlanVersion = b.PlanVersion
	approval, e := r.Execution().Engine().ApprovalManager().RequestApproval(req)
	if e != nil {
		t.Fatal(e)
	}
	if e := r.Execution().Engine().ApprovalManager().Approve(approval.ApprovalID, "fixture-old-owner", "unit fixture only, not real native approval", time.Now()); e != nil {
		t.Fatal(e)
	}
	run, e := r.Execution().Engine().GetRun(ctx, b.Run)
	if e != nil {
		t.Fatal(e)
	}
	task := run.Tasks[b.Task]
	task.ApprovalID = approval.ApprovalID
	task.ApprovalRequired = true
	task.Attempts = 2
	run.Tasks[b.Task] = task
	if e := r.Execution().Engine().RunStore().UpdateRun(ctx, run); e != nil {
		t.Fatal(e)
	}
	root := r.layout.Root
	r.Close()
	reopened, e := Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	reopenedRun, e := reopened.Execution().Engine().GetRun(ctx, b.Run)
	if e != nil {
		t.Fatal(e)
	}
	task = reopenedRun.Tasks[b.Task]
	p, e := reopened.Store().GetPlan(ctx, b.Plan, b.PlanVersion)
	if e != nil {
		t.Fatal(e)
	}
	goal, e := reopened.Store().GetGoalContract(ctx, reopenedRun.GoalID, reopenedRun.GoalRevision)
	if e != nil {
		t.Fatal(e)
	}
	pkg, e := execution.BuildConstraintPackage(goal, task, p)
	if e != nil {
		t.Fatal(e)
	}
	pkg.ApprovalRequired = true
	pkg.Digest = execution.ComputeConstraintDigest(pkg)
	_, _, e = reopened.reviewProviderTransport(ctx, task, pkg, c, agent, "agy", b.Model, task.WorktreePath)
	if !errors.Is(e, execution.ErrApprovalTOCTOUViolation) {
		t.Fatalf("stored primary-only approval not refused before authority: %v", e)
	}
	preserved, e := reopened.Execution().Engine().ApprovalManager().GetApproval(approval.ApprovalID)
	if e != nil || preserved.Status != execution.ApprovalApproved || preserved.Scope != req.Scope || preserved.TargetResource != "agy" {
		t.Fatal("historical approval rewritten or consumed")
	}
}
