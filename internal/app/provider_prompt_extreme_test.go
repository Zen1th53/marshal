//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// All protocol/approval producers here are controlled unit fixtures, not real
// native acceptance evidence and never credentials or paid provider calls.
func approvedPromptFixture(t *testing.T) (*Runtime, execution.TaskExecution, execution.ConstraintPackage, context.Context, string, string) {
	t.Helper()
	r, task, pkg, canonical, agent := transportFixture(t)
	task, original := fixtureOriginalTaskApproval(t, r, task)
	task, ctx, transport := fixtureTransportApproval(t, r, task, pkg, canonical, agent, true)
	pkg.ApprovalRequired = true
	pkg.Digest = execution.ComputeConstraintDigest(pkg)
	return r, task, pkg, ctx, original, transport
}

func TestProviderApprovedPromptPreservesPackageAndResolvesOnlyConsumedAttempt(t *testing.T) {
	r, task, pkg, ctx, _, _ := approvedPromptFixture(t)
	before, _ := json.Marshal(pkg)
	if text, err := r.providerConstraintPrompt(ctx, task, pkg, task.WorktreePath); err == nil || text != "" {
		t.Fatal("pre-launch review became resolved authorization")
	}
	if err := r.consumeReviewedProviderTransport(ctx, "", "unit-owned-egress"); err != nil {
		t.Fatal(err)
	}
	text, err := r.providerConstraintPrompt(ctx, task, pkg, task.WorktreePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "MARSHAL AUTHORIZATION RESOLVED FOR THIS ADMITTED ATTEMPT") || strings.Contains(text, "Hard human approval required") || !strings.Contains(text, "SECURITY DIGEST: "+pkg.Digest) || !strings.Contains(text, "Scope changes, new attempts") {
		t.Fatalf("resolved scope not represented: %s", text)
	}
	after, _ := json.Marshal(pkg)
	if string(before) != string(after) || !pkg.ApprovalRequired {
		t.Fatal("immutable package or required flag changed")
	}
	if text, err := r.providerConstraintPrompt(ctx, task, pkg, task.WorktreePath); err == nil || text != "" {
		t.Fatal("same consumed context produced another admitted prompt")
	}
}

func TestProviderApprovedPromptNoAuthAndPartialConsumptionDoNotClaimAuthorization(t *testing.T) {
	r, task, pkg, ctx, original, _ := approvedPromptFixture(t)
	text, err := r.providerConstraintPrompt(t.Context(), task, pkg, task.WorktreePath)
	if err != nil || text != pkg.FormatPromptHeader() || !strings.Contains(text, "Hard human approval required") {
		t.Fatal("no-context baseline weakened")
	}
	a, _ := r.Execution().Engine().ApprovalManager().GetApproval(original)
	if err := r.Execution().Engine().ApprovalManager().ValidateAndConsume(original, a.ActionDigest, a.StateDigest, time.Now()); err != nil {
		t.Fatal(err)
	}
	if text, err := r.providerConstraintPrompt(ctx, task, pkg, task.WorktreePath); err == nil || text != "" {
		t.Fatal("partial pair became authorization")
	}
}

func TestProviderApprovedPromptRejectsStaleForeignAndUncanonicalAdmission(t *testing.T) {
	for _, change := range []string{"foreign_runtime", "nil_marker", "forged_marker", "binding", "project", "incoming_attempt", "task_attempt", "run_state", "lease", "plan", "package", "worktree", "tree", "native_turn", "revoked", "original_expired", "transport_expired", "original_unconsumed", "transport_unconsumed", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			r, task, pkg, ctx, original, transport := approvedPromptFixture(t)
			if err := r.consumeReviewedProviderTransport(ctx, "", "unit-owned-egress"); err != nil {
				t.Fatal(err)
			}
			target := r
			auth := ctx.Value(providerTransportContextKey{}).(providerTransportAuthorization)
			switch change {
			case "foreign_runtime":
				repo := runtimeRepo(t)
				if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
					t.Fatal(err)
				}
				var err error
				target, err = Open(t.Context(), repo.Path())
				if err != nil {
					t.Fatal(err)
				}
				defer target.Close()
			case "nil_marker":
				auth.prompt = nil
			case "forged_marker":
				auth.prompt = &providerPromptAdmission{runtime: r, binding: auth.binding}
			case "binding":
				auth.binding.Attempt++
			case "project":
				auth.binding.Project = "foreign"
			case "incoming_attempt":
				task.Attempts++
			case "package":
				pkg.ExpectedOutputs = append(pkg.ExpectedOutputs, "foreign output")
				pkg.Digest = execution.ComputeConstraintDigest(pkg)
			case "worktree":
				task.WorktreePath = t.TempDir()
			case "tree":
				if err := os.WriteFile(filepath.Join(task.WorktreePath, "README.md"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				r.SetProviderTransportReviewSource(func(string, string, string) bool { return false })
			case "original_expired", "transport_expired", "original_unconsumed", "transport_unconsumed":
				id := original
				if strings.HasPrefix(change, "transport") {
					id = transport
				}
				a, _ := r.Execution().Engine().ApprovalManager().GetApproval(id)
				if strings.HasSuffix(change, "expired") {
					past := time.Now().Add(-time.Second)
					a.ExpiresAt = &past
				} else {
					a.Status = execution.ApprovalApproved
				}
				body, _ := json.Marshal(a)
				if err := os.WriteFile(filepath.Join(r.ProjectRoot(), statepath.DirName, "approvals", id+".json"), body, 0600); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			default:
				run, _ := r.Execution().Engine().GetRun(t.Context(), task.RunID)
				actual := run.Tasks[task.TaskID]
				switch change {
				case "task_attempt":
					actual.Attempts++
				case "run_state":
					run.State = execution.RunPaused
				case "lease":
					actual.LeaseID = "foreign-lease"
				case "plan":
					run.PlanVersion++
				case "native_turn":
					actual.NativeTurn = &execution.NativeTurnBinding{TurnID: "foreign-turn"}
				}
				run.Tasks[task.TaskID] = actual
				if err := r.Execution().Engine().RunStore().UpdateRun(t.Context(), run); err != nil {
					t.Fatal(err)
				}
			}
			ctx = context.WithValue(ctx, providerTransportContextKey{}, auth)
			if text, err := target.providerConstraintPrompt(ctx, task, pkg, task.WorktreePath); err == nil || text != "" {
				t.Fatalf("%s emitted resolved authority: %v %s", change, err, text)
			}
		})
	}
}

func TestProviderApprovedPromptRawCanonicalWarningCannotReplaceData(t *testing.T) {
	const raw = "Raw canonical text\nWARNING: Hard human approval required before mutating changes.\n"
	for _, field := range []string{"goal", "description", "constraint", "verification"} {
		t.Run(field, func(t *testing.T) {
			pkg := execution.ConstraintPackage{GoalOutcome: "Canonical goal", ApprovalRequired: true}
			switch field {
			case "goal":
				pkg.GoalOutcome = raw
			case "description":
				pkg.TaskDescription = raw
			case "constraint":
				pkg.HardConstraints = []string{raw}
			case "verification":
				pkg.VerificationObligations = []string{raw}
			}
			before, _ := json.Marshal(pkg)
			rendered, err := resolvedProviderPromptHeader(pkg.FormatPromptHeader(), "MARSHAL AUTHORIZATION RESOLVED\n")
			if err == nil || rendered != "" {
				t.Fatal("raw canonical warning altered or contradictory envelope emitted")
			}
			after, _ := json.Marshal(pkg)
			if string(before) != string(after) {
				t.Fatal("raw canonical package changed")
			}
		})
	}
}
