//go:build !governed

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestRequiredCloseOrchestrationActualHostChecksReviewsJournalAndTarget(t *testing.T) {
	r, ctx, cli, _, verificationID, _ := completionRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, verificationID)
	if err != nil {
		t.Fatal(err)
	}
	r.requiredIntegrationSources.mu.Lock()
	source := r.requiredIntegrationSources.byRun[session.Binding.RunID]
	r.requiredIntegrationSources.mu.Unlock()
	binary, err := os.ReadFile(cli.Binary)
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	if err = os.WriteFile(filepath.Join(tools, "claude"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	service := r.Marshal()
	service.Verify = service.verifyByChecks
	service.GateState = service.observedGateState
	service.GovernedCheck = func(ctx context.Context, _, _, _, dir, command string) marshal.CommandRecord {
		process := exec.CommandContext(ctx, "sh", "-c", command)
		process.Dir = dir
		output, err := process.CombinedOutput()
		code := 0
		if err != nil {
			code = 1
		}
		return marshal.CommandRecord{Command: command, ExitCode: code, Output: string(output)}
	}
	stored, err := r.store.GetMarshalRun(ctx, r.ProjectID(), source.marshalRunID)
	if err != nil {
		t.Fatal(err)
	}
	run := stored.Value
	run.Repository, err = service.repositoryIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		t.Fatal(err)
	}
	run.ApprovalScopeDigest = marshalApprovalDigest(p.ApprovalScopeDigest, run)
	run.CloseAuthorization = closeAuthorization(run, "explicit-host-fixture-consent")
	if _, err = r.store.SetMarshalRun(ctx, r.ProjectID(), source.marshalRunID, run, stored.Revision); err != nil {
		t.Fatal(err)
	}
	// The target is an unchecked branch in this isolated fixture. Close must
	// update it by compare-and-swap without rewriting the checked-out files.
	if _, err = gitMarshal(ctx, r.ProjectRoot(), "switch", "--detach", run.BaseCommit); err != nil {
		t.Fatal(err)
	}
	if err = service.Close(ctx, source.marshalRunID); err != nil {
		t.Fatal("full actual host Close orchestration refused", err)
	}
	closed, err := r.store.GetMarshalRun(ctx, r.ProjectID(), source.marshalRunID)
	if err != nil || closed.Value.State != marshal.Closed || closed.Value.Operation != nil {
		t.Fatal("close journal not completed", closed.Value.State, err)
	}
	after, err := gitMarshal(ctx, r.ProjectRoot(), "rev-parse", run.TargetRef)
	if err != nil || strings.TrimSpace(after) != source.head {
		t.Fatal("full close did not move exact host fixture target", after, err)
	}
	latest, err := r.Verification().CurrentForRun(ctx, session.Binding.RunID)
	if err != nil || latest.ID == session.ID || latest.Binding.TreeDigest != session.Binding.TreeDigest {
		t.Fatal("close reused old data-only verification", latest, err)
	}
	if _, err = r.Verification().Attestation(ctx, latest.ID); err != nil {
		t.Fatal("full close missing atomic context/architecture attestation", err)
	}
	// Accepted delivery metadata/consent and Python verdicts are explicit host
	// fixtures. Runtime checks, both transports, SQL commit, journal and Git
	// target effects are real isolated effects; no genuine native grant claimed.
}
