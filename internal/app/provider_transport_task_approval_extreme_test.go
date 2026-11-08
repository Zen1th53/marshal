//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These are owned unit approvals/protocol fixtures, not native model evidence.
func fixtureOriginalTaskApproval(t *testing.T, r *Runtime, task execution.TaskExecution, ttl ...time.Duration) (execution.TaskExecution, string) {
	t.Helper()
	run, err := r.Execution().Engine().GetRun(t.Context(), task.RunID)
	if err != nil {
		t.Fatal(err)
	}
	duration := time.Duration(0)
	if len(ttl) > 0 {
		duration = ttl[0]
	}
	a, err := r.Execution().Engine().ApprovalManager().RequestApproval(execution.ApprovalRequest{TTL: duration, RunID: run.RunID, TaskID: task.TaskID, PlanID: run.PlanID, PlanVersion: run.PlanVersion, OperationType: "TASK_EXECUTE", TargetResource: filepath.Join(task.TargetFiles...), RiskLevel: model.R2, Scope: "task_mutation", Parameters: task.Description})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Execution().Engine().ApprovalManager().Approve(a.ApprovalID, "unit-owner", "fixture, not model credit", time.Now()); err != nil {
		t.Fatal(err)
	}
	task.ApprovalID = a.ApprovalID
	task.ApprovalRequired = true
	run.Tasks[task.TaskID] = task
	if err := r.Execution().Engine().RunStore().UpdateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	return task, a.ApprovalID
}

func TestProviderTransportOriginalTaskApprovalFreshness(t *testing.T) {
	for _, change := range []string{"expired", "expiry_changed", "denied", "consumed", "task", "plan", "version", "action", "state", "description", "targets", "origin_missing", "origin_downgrade", "old_version", "native_turn"} {
		t.Run(change, func(t *testing.T) {
			r, task, pkg, canonical, agent := transportFixture(t)
			task, originID := fixtureOriginalTaskApproval(t, r, task)
			task, granted, transportID := fixtureTransportApproval(t, r, task, pkg, canonical, agent, true)
			origin, _ := r.Execution().Engine().ApprovalManager().GetApproval(originID)
			beforeTransport, _ := r.Execution().Engine().ApprovalManager().GetApproval(transportID)
			auth := granted.Value(providerTransportContextKey{}).(providerTransportAuthorization)
			switch change {
			case "description", "targets", "native_turn":
				run, _ := r.Execution().Engine().GetRun(t.Context(), task.RunID)
				if change == "description" {
					task.Description = "changed action"
				}
				if change == "targets" {
					task.TargetFiles = []string{"different.txt"}
				}
				if change == "native_turn" {
					task.NativeTurn = &execution.NativeTurnBinding{TurnID: "already-started"}
				}
				run.Tasks[task.TaskID] = task
				if err := r.Execution().Engine().RunStore().UpdateRun(t.Context(), run); err != nil {
					t.Fatal(err)
				}
			case "origin_downgrade":
				auth.binding.Original = providerTransportTaskApproval{}
			case "origin_missing":
				auth.binding.Original = providerTransportTaskApproval{Required: true}
			case "old_version":
				auth.binding.Version = 0
			default:
				switch change {
				case "expired":
					past := time.Now().Add(-time.Second)
					origin.ExpiresAt = &past
				case "expiry_changed":
					future := origin.ExpiresAt.Add(time.Minute)
					origin.ExpiresAt = &future
				case "denied":
					origin.Status = execution.ApprovalDenied
				case "consumed":
					origin.Status = execution.ApprovalConsumed
				case "task":
					origin.TaskID = "foreign"
				case "plan":
					origin.PlanID = "foreign"
				case "version":
					origin.PlanVersion++
				case "action":
					origin.ActionDigest = "foreign"
				case "state":
					origin.StateDigest = "foreign"
				}
				body, _ := json.Marshal(origin)
				if err := os.WriteFile(filepath.Join(r.ProjectRoot(), statepath.DirName, "approvals", originID+".json"), body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			granted = context.WithValue(granted, providerTransportContextKey{}, auth)
			if err := r.consumeReviewedProviderTransport(granted, "127.0.0.1:11434", "must-not-start"); err == nil {
				t.Fatal("stale ordinary authorization admitted a provider")
			}
			after, _ := r.Execution().Engine().ApprovalManager().GetApproval(transportID)
			if after.Status != beforeTransport.Status || after.Revision != beforeTransport.Revision {
				t.Fatal("refusal consumed or rewrote transport")
			}
			freshOrigin, _ := r.Execution().Engine().ApprovalManager().GetApproval(originID)
			if freshOrigin.Status != origin.Status || freshOrigin.Revision != origin.Revision {
				t.Fatal("refusal consumed or rewrote original task grant")
			}
		})
	}
}
func TestProviderTransportOriginalTaskApprovalExpiryBoundary(t *testing.T) {
	r, task, pkg, canonical, agent := transportFixture(t)
	task, _ = fixtureOriginalTaskApproval(t, r, task)
	task, granted, _ := fixtureTransportApproval(t, r, task, pkg, canonical, agent, true)
	b := granted.Value(providerTransportContextKey{}).(providerTransportAuthorization).binding
	run, _ := r.Execution().Engine().GetRun(t.Context(), task.RunID)
	if err := r.validateTransportTaskApproval(b, run, task, b.Original.Expires.Add(-time.Nanosecond)); err != nil {
		t.Fatalf("live exact original refused: %v", err)
	}
	for _, now := range []time.Time{b.Original.Expires, b.Original.Expires.Add(time.Nanosecond)} {
		if err := r.validateTransportTaskApproval(b, run, task, now); err == nil {
			t.Fatal("boundary/past expiry accepted")
		}
	}
}
func TestProviderTransportOriginalTaskApprovalReopenAndOneShot(t *testing.T) {
	r, task, pkg, canonical, agent := transportFixture(t)
	task, originID := fixtureOriginalTaskApproval(t, r, task)
	_, granted, transportID := fixtureTransportApproval(t, r, task, pkg, canonical, agent, true)
	root := r.ProjectRoot()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.SetProviderTransportReviewSource(func(launch, owner, provider string) bool {
		return launch == "fixture-launch" && owner == "fixture-owner" && provider == "codex"
	})
	if err := reopened.consumeReviewedProviderTransport(granted, "", "unit-reopen"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{originID, transportID} {
		a, _ := reopened.Execution().Engine().ApprovalManager().GetApproval(id)
		if a.Status != execution.ApprovalConsumed {
			t.Fatalf("pair not consumed %s: %+v", id, a)
		}
	}
	if err := reopened.consumeReviewedProviderTransport(granted, "", "replay"); err == nil {
		t.Fatal("reopened consumed pair replayed")
	}
}
func TestProviderTransportRequiredOriginalMissingFailsClosed(t *testing.T) {
	r, task, pkg, canonical, agent := transportFixtureWithTaskApproval(t, true)
	if _, pause, err := r.reviewProviderTransport(t.Context(), task, pkg, canonical, agent, "opencode", "ollama/test", task.WorktreePath); err == nil || pause != nil {
		t.Fatalf("required missing original created new transport: %v %+v", err, pause)
	}
}

func TestProviderTransportPartialPairConsumptionIsConservative(t *testing.T) {
	r, task, pkg, canonical, agent := transportFixture(t)
	task, originID := fixtureOriginalTaskApproval(t, r, task)
	_, granted, transportID := fixtureTransportApproval(t, r, task, pkg, canonical, agent, true)
	transport, _ := r.Execution().Engine().ApprovalManager().GetApproval(transportID)
	past := time.Now().Add(-time.Second)
	transport.ExpiresAt = &past
	raw, _ := json.Marshal(transport)
	if err := os.WriteFile(filepath.Join(r.ProjectRoot(), statepath.DirName, "approvals", transportID+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.consumeReviewedProviderTransport(granted, "", "must-not-start"); err == nil {
		t.Fatal("expired second grant started provider")
	}
	origin, _ := r.Execution().Engine().ApprovalManager().GetApproval(originID)
	transport, _ = r.Execution().Engine().ApprovalManager().GetApproval(transportID)
	if origin.Status != execution.ApprovalConsumed || transport.Status == execution.ApprovalConsumed {
		t.Fatalf("partial consumption rolled back or falsely succeeded: origin=%+v transport=%+v", origin, transport)
	}
	if err := r.consumeReviewedProviderTransport(granted, "", "must-not-replay"); err == nil {
		t.Fatal("partially spent pair replayed")
	}
}
