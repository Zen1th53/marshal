package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
)

func TestMarshalApprovalsFiltersBoundRunAndRedacts(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	service := runtime.Marshal()
	if service == nil || service.ProjectID == "" {
		t.Skip("no Marshal service in this edition")
	}

	// Create two distinct Marshal runs: one bound to the MCP server, one foreign.
	boundRun := marshal.Run{
		PlanID:      "PLAN-bound",
		PlanVersion: 1,
		Settings:    marshal.DefaultSettings(),
		State:       marshal.AwaitingUser,
		Tasks: []marshal.Task{
			{PlanTaskID: "task-bound", Title: "Bound task", Worker: "codex", Mode: marshal.Governed, State: marshal.Queued},
		},
	}
	foreignRun := marshal.Run{
		PlanID:      "PLAN-foreign",
		PlanVersion: 1,
		Settings:    marshal.DefaultSettings(),
		State:       marshal.AwaitingUser,
		Tasks: []marshal.Task{
			{PlanTaskID: "task-foreign", Title: "Foreign task", Worker: "codex", Mode: marshal.Governed, State: marshal.Queued},
		},
	}
	if _, err := service.Store.SetMarshalRun(ctx, service.ProjectID, "RUN-bound", boundRun, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.SetMarshalRun(ctx, service.ProjectID, "RUN-foreign", foreignRun, 0); err != nil {
		t.Fatal(err)
	}

	mgr := runtime.Execution().Engine().ApprovalManager()
	now := time.Now().UTC().Truncate(time.Second)
	secretToken := "sk-proj-secret1234567890abcdef1234567890"
	boundApp, err := mgr.RequestApproval(execution.ApprovalRequest{
		RequestedBy:    "worker-bound",
		RunID:          "exec-bound",
		TaskID:         "task-bound",
		PlanID:         "PLAN-bound",
		PlanVersion:    1,
		Attempt:        2,
		OperationType:  "TASK_EXECUTE",
		TargetResource: "config.json Bearer " + secretToken,
		RiskLevel:      model.R2,
		Scope:          "task_mutation password=supersecretvalue",
		Parameters:     "Authorization: Bearer " + secretToken,
		TTL:            15 * time.Minute,
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	foreignApp, err := mgr.RequestApproval(execution.ApprovalRequest{
		RequestedBy:    "worker-foreign",
		RunID:          "exec-foreign",
		TaskID:         "task-foreign",
		PlanID:         "PLAN-foreign",
		PlanVersion:    1,
		Attempt:        1,
		OperationType:  "TASK_EXECUTE",
		TargetResource: "other.txt",
		RiskLevel:      model.R2,
		Scope:          "task_mutation",
		Parameters:     "foreign task",
		TTL:            15 * time.Minute,
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.RecordApprovalOpened(ctx, "RUN-bound", *boundApp); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RecordApprovalOpened(ctx, "RUN-foreign", *foreignApp); err != nil {
		t.Fatal(err)
	}

	// Pin the server to RUN-bound.
	srv := NewServer(runtime)
	srv.SetBoundRunID("RUN-bound")

	listed := false
	for _, tool := range srv.listTools() {
		if tool.Name == "marshal_approvals" {
			listed = true
			break
		}
	}
	if !listed {
		t.Fatal("marshal_approvals tool is not listed")
	}

	// Capture approval file bytes to verify read-only pull never mutates canonical storage.
	boundPath := filepath.Join(runtime.ProjectRoot(), statepath.DirName, "approvals", boundApp.ApprovalID+".json")
	beforeBytes, err := os.ReadFile(boundPath)
	if err != nil {
		t.Fatal(err)
	}

	caller := auth.Principal{ID: "marshal-test"}
	raw, err := srv.callTool(ctx, caller, "marshal_approvals", nil)
	if err != nil {
		t.Fatalf("marshal_approvals call failed: %v", err)
	}
	if strings.Contains(raw, secretToken) || strings.Contains(raw, "supersecretvalue") {
		t.Fatalf("marshal_approvals leaked unredacted secret: %s", raw)
	}
	if strings.Contains(raw, foreignApp.ApprovalID) || strings.Contains(raw, "task-foreign") {
		t.Fatalf("marshal_approvals leaked foreign run approval: %s", raw)
	}

	var resp marshalApprovalsResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode response: %v (raw=%s)", err, raw)
	}
	if resp.NoRun || resp.RunID != "RUN-bound" {
		t.Fatalf("unexpected run binding: %+v", resp)
	}
	if len(resp.Approvals) != 1 {
		t.Fatalf("expected 1 bound approval, got %d: %+v", len(resp.Approvals), resp.Approvals)
	}
	got := resp.Approvals[0]
	if got.ApprovalID != boundApp.ApprovalID || got.RunID != "exec-bound" || got.TaskID != "task-bound" ||
		got.Attempt != 2 || got.ScopeDigest == "" || got.CreatedAt.IsZero() || got.ExpiresAt == nil {
		t.Fatalf("incomplete authoritative identity: %+v", got)
	}
	if got.Status != execution.ApprovalRequested || got.Expired {
		t.Fatalf("expected live REQUESTED approval, got: %+v", got)
	}

	// Edition-specific destination, command, and wording assertions.
	if statepath.PublicEngine {
		// Default edition (!governed)
		if got.Authority != "marshal" || got.WhoCanAnswer != "marshal" {
			t.Fatalf("default edition authority = %q/%q, want marshal", got.Authority, got.WhoCanAnswer)
		}
		if !strings.Contains(got.Command, "/marshal approve-task "+boundApp.ApprovalID) {
			t.Fatalf("default edition command = %q, want /marshal approve-task %s", got.Command, boundApp.ApprovalID)
		}
		if !strings.Contains(got.OwnerNotice, "Marshal") || !strings.Contains(got.MarshalNotice, "Marshal") {
			t.Fatalf("default edition notices must name who can answer: owner=%q marshal=%q", got.OwnerNotice, got.MarshalNotice)
		}
	} else {
		// Governed edition (-tags governed)
		if got.Authority != "owner" || got.WhoCanAnswer != "owner" {
			t.Fatalf("governed edition authority = %q/%q, want owner", got.Authority, got.WhoCanAnswer)
		}
		if !strings.Contains(got.Command, "/approve execution:"+boundApp.ApprovalID) {
			t.Fatalf("governed edition command = %q, want /approve execution:%s", got.Command, boundApp.ApprovalID)
		}
		if !strings.Contains(got.OwnerNotice, "owner") || !strings.Contains(got.MarshalNotice, "Only the owner can answer") {
			t.Fatalf("governed edition notices must name owner authority: owner=%q marshal=%q", got.OwnerNotice, got.MarshalNotice)
		}
	}

	// Requesting a foreign run_id explicitly must be denied.
	if _, err := srv.callTool(ctx, caller, "marshal_approvals", map[string]any{"run_id": "RUN-foreign"}); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied for foreign run_id, got: %v", err)
	}

	// Read-only guarantee: canonical approval file bytes are untouched.
	afterBytes, err := os.ReadFile(boundPath)
	if err != nil || !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatal("read-only marshal_approvals mutated approval file bytes")
	}

	// Verify durable notices are scoped to RUN-bound and redacted.
	if len(resp.Notices) != 1 || resp.Notices[0].ApprovalID != boundApp.ApprovalID || resp.Notices[0].Transition != "opened" {
		t.Fatalf("expected 1 bound opened notice, got: %+v", resp.Notices)
	}

	// Verify worker and session roles cannot call marshal_approvals.
	for _, role := range []string{"worker", "session"} {
		scoped := NewServer(runtime)
		scoped.role = role
		if role == "worker" {
			scoped.workerTaskID = "task-bound"
		}
		if _, err := scoped.callTool(ctx, caller, "marshal_approvals", nil); !errors.Is(err, model.ErrPolicyDenied) {
			t.Fatalf("role %q unexpectedly allowed marshal_approvals: %v", role, err)
		}
	}

	// Verify run_digest surfaces pending approval summary for the active run.
	srv.SetBoundRunID("")
	digestRaw, err := srv.callTool(ctx, caller, "run_digest", nil)
	if err != nil {
		t.Fatalf("run_digest failed: %v", err)
	}
	var digest runDigest
	if err := json.Unmarshal([]byte(digestRaw), &digest); err != nil {
		t.Fatalf("decode run_digest: %v", err)
	}
	if digest.PendingApprovals != 1 || len(digest.Approvals) != 1 {
		t.Fatalf("expected 1 pending approval in run_digest, got: %+v", digest)
	}
}
