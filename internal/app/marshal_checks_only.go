package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

// checksOnlyDelivery reports whether the run's approved review policy opts
// out of the final independent model check. Only that approved snapshot
// selects it: an unavailable verifier never does, and the governed edition
// resolves every weakened policy back to the shipped defaults.
func checksOnlyDelivery(run marshal.Run) bool {
	return run.EffectiveReviewPolicy().FinalModel == marshal.FinalModelOff
}

func checksOnlyDigest(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// checksOnlyOmittedRoles lists the review roles the approved policy left out.
func checksOnlyOmittedRoles(run marshal.Run) []string {
	policy := run.EffectiveReviewPolicy()
	roles := []string{"independent-verifier"}
	if policy.CrossReview == marshal.CrossReviewOff {
		roles = append(roles, "cross-review")
	}
	return roles
}

// runChecksOnlyReceipt reruns every approved check, in approved order, on
// the integration worktree at head and seals what the runtime observed. A
// check that fails, cannot run or changes the checkout is recorded as not
// passing, so the receipt blocks delivery.
func (s *MarshalService) runChecksOnlyReceipt(ctx context.Context, runID string, run marshal.Run, head, authority string) (verification.ChecksOnlyReceipt, error) {
	dir := filepath.Join(s.Worktrees, integrationTaskID(runID, run))
	tree, err := gitMarshal(ctx, dir, "rev-parse", head+"^{tree}")
	if err != nil {
		return verification.ChecksOnlyReceipt{}, err
	}
	// The same pre-run admission the full path takes: a clean checkout at the
	// exact head and, for governed work, its retained honeypot admissions.
	governed := false
	for _, t := range run.Tasks {
		if t.Mode == marshal.Governed {
			governed = true
		}
	}
	if _, err := s.beginMarshalRuntimeProof(ctx, dir, run, head, tree, governed); err != nil {
		return verification.ChecksOnlyReceipt{}, err
	}
	type approvedCheck struct {
		ID, Task, Command string
		Criteria          []string
	}
	var approved []approvedCheck
	var observations []verification.ChecksOnlyObservation
	for _, t := range run.Tasks {
		for i, c := range t.Checks {
			id := fmt.Sprintf("%s#%d", t.PlanTaskID, i)
			approved = append(approved, approvedCheck{ID: id, Task: t.PlanTaskID, Command: c.Command, Criteria: c.Criteria})
			observation := verification.ChecksOnlyObservation{ID: id, Task: t.PlanTaskID, Command: c.Command, Status: verification.StatusUnknown, ExitCode: -1}
			var runner driver.CheckRunner
			if t.Mode == marshal.Governed {
				if s.GovernedCheck == nil {
					observations = append(observations, observation)
					continue
				}
				task := t
				runner = func(ctx context.Context, dir, command string) marshal.CommandRecord {
					return s.GovernedCheck(ctx, runID, task.PlanTaskID, task.Worker, dir, command)
				}
			}
			record, checkErr := runIntegrationCheckObserved(ctx, dir, head, c.Command, runner)
			observation.ExitCode = record.ExitCode
			sum := sha256.Sum256([]byte(record.Output))
			observation.OutputDigest = "sha256:" + hex.EncodeToString(sum[:])
			switch {
			case checkErr == nil && record.ExitCode == 0:
				observation.Status = verification.StatusPass
			case record.ExitCode != 0:
				observation.Status = verification.StatusFail
			default:
				observation.Status = verification.StatusBlocked
			}
			observations = append(observations, observation)
		}
	}
	if len(approved) == 0 {
		return verification.ChecksOnlyReceipt{}, errors.New("marshal: no approved checks to verify the integrated result")
	}
	if after, err := gitMarshal(ctx, dir, "rev-parse", "HEAD"); err != nil || after != head {
		return verification.ChecksOnlyReceipt{}, errors.New("the integration branch moved during verification")
	}
	id, err := model.NewID("CHECKS-ONLY-")
	if err != nil {
		return verification.ChecksOnlyReceipt{}, err
	}
	actor := ""
	if run.ValidCloseAuthorization() {
		actor = run.CloseAuthorization.User
	}
	receipt := verification.ChecksOnlyReceipt{
		ID: id, ProjectID: s.ProjectID, RunID: runID, GoalID: run.GoalBinding,
		PlanID: run.PlanID, PlanVersion: run.PlanVersion, ArtifactRevision: run.ArtifactRevision,
		PlanDigest: run.ApprovalScopeDigest, PolicyDigest: checksOnlyDigest(run.EffectiveReviewPolicy()),
		TargetRef: run.TargetRef, TargetBefore: run.BaseCommit, Head: head, Tree: tree,
		ChecksDigest: checksOnlyDigest(approved), Observations: observations,
		Actor: actor, Authority: authority,
		OmittedRoles: checksOnlyOmittedRoles(run), CreatedAt: s.clock(),
	}
	receipt.Digest = receipt.ComputeDigest()
	return receipt, nil
}

// checksOnlyReceiptCurrent reports whether a retained receipt still binds the
// run's current approval, revisions and policy.
func checksOnlyReceiptCurrent(run marshal.Run, receipt verification.ChecksOnlyReceipt) bool {
	return receipt.PlanID == run.PlanID && receipt.PlanVersion == run.PlanVersion && receipt.ArtifactRevision == run.ArtifactRevision &&
		receipt.PlanDigest == run.ApprovalScopeDigest && receipt.PolicyDigest == checksOnlyDigest(run.EffectiveReviewPolicy()) &&
		receipt.TargetBefore == run.BaseCommit && receipt.TargetRef == run.TargetRef && checksOnlyDelivery(run) && receipt.Passed()
}

// checksOnlySession presents a receipt's observations the way a failed
// verification pause reads them. It is never stored as a session.
func checksOnlySession(receipt verification.ChecksOnlyReceipt) verification.Session {
	var session verification.Session
	for _, o := range receipt.Observations {
		session.Claims = append(session.Claims, verification.Claim{ID: o.ID, CriterionID: o.ID, SemanticScope: []string{o.Command}, EvidenceIDs: []string{o.ID}})
		session.Evidence = append(session.Evidence, verification.Evidence{ID: o.ID, ClaimID: o.ID, Status: o.Status, Command: []string{o.Command}})
	}
	return session
}

// verifyChecksOnly is VerifyMerged for a run that opted out of the final
// independent model check: the approved checks rerun on the exact integrated
// head with the mandatory source admission, and a pass leaves the run
// verifying with its receipt. No verifier, session or attestation is used.
func (s *MarshalService) verifyChecksOnly(ctx context.Context, runID string, run marshal.Run, rev int64, charge marshal.Charge) (verification.Decision, error) {
	head, err := gitMarshal(ctx, filepath.Join(s.Worktrees, integrationTaskID(runID, run)), "rev-parse", "HEAD")
	if err != nil {
		return verification.Blocked, err
	}
	s.codeEvidenceVerify(ctx, runID, run, head)
	if err := s.settleFinishedExecution(ctx, run); err != nil {
		return verification.Blocked, err
	}
	if s.requiredIntegrationSource != nil {
		if err := s.requiredIntegrationSource(ctx, runID, run, head); err != nil {
			return verification.Blocked, err
		}
	}
	receipt, err := s.runChecksOnlyReceipt(ctx, runID, run, head, "verification")
	if err != nil {
		return verification.Blocked, err
	}
	result := verification.ChecksOnlyPassed
	if !receipt.Passed() {
		result = verification.VerificationFailed
	}
	budget, err := s.Charge(ctx, runID, "", "verification", charge)
	if err != nil {
		return result, err
	}
	if len(budget.PlanExceeded) > 0 || budgetNeedsOperator(run.Budget, budget) {
		paused, pausedRev, loadErr := s.load(ctx, runID)
		if loadErr != nil {
			return result, loadErr
		}
		pauseMarshalBudget(&paused, budget)
		return result, s.save(ctx, runID, paused, pausedRev)
	}
	if result == verification.ChecksOnlyPassed {
		run.ChecksOnly = &receipt
		if run.Compatibility != nil {
			run.Compatibility.Disposition = "reviewed-current-runtime"
		}
	} else {
		run.ChecksOnly = nil
		pauseMarshal(&run, verificationFailedPause(checksOnlySession(receipt), result))
	}
	return result, s.save(ctx, runID, run, rev)
}
