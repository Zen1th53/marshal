//go:build !governed

package store

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/verification"
)

func projectCompletionFixture(t *testing.T) (*Store, []ProjectContextAdmission, verification.BundleEnvelope, verification.CompletionAttestation, projectcontext.Receipt) {
	t.Helper()
	s, c, audience, now := acceptedContextStore(t)
	// This policy applies ONLY to the second task. An empty parent TaskID or
	// project-wide-only packet cannot cover it.
	r := contextStoreRecord("MEM-inspect-task-only", now)
	r.Scope, r.ScopeID = "task", "inspect"
	r.ContentDigest = r.CanonicalDigest()
	if err := s.WriteMemoryV2(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	previous, _ := c.CanonicalDigest()
	audience.ScopeIDs = append(audience.ScopeIDs, r.ScopeID)
	_, inventory, err := s.ProjectContractInventory(t.Context(), c.Project, audience)
	if err != nil {
		t.Fatal(err)
	}
	c.Revision, c.RevisionReason, c.SupersedesDigest, c.ReviewedInventoryDigest = 2, "Reviewed task-only inspection policy", previous, inventory
	c.Required = append(c.Required, projectcontext.MemoryRef{ID: r.ID, ProjectID: r.ProjectID, Scope: r.Scope, ScopeID: r.ScopeID, Revision: r.Revision, ContentDigest: r.ContentDigest, StateFingerprint: projectcontext.MemoryFingerprint(r), Class: projectcontext.Policy, Applies: projectcontext.Applicability{TaskIDs: []string{"inspect"}}, Reason: "Required only by result inspection", AcceptanceOccurrence: "supported-inspect-policy"})
	req := admitContextDecision(t, s, c, true, "inspect-policy")
	c, err = s.CommitMCPProjectContractDecision(t.Context(), req, true, audience, now, contextAudit(req, now))
	if err != nil {
		t.Fatal(err)
	}
	a := contextAdmission(t, s, c, audience, now)
	p := storedPlan()
	p.Tasks[0].Paths = []string{"internal/store"}
	p.Tasks = append(p.Tasks, plan.Task{ID: "inspect", Title: "Check the result", Paths: []string{"internal/store"}, Weight: 1})
	p.Graph.Order = []string{"implement", "inspect"}
	if err := s.SavePlan(t.Context(), p, 0); err != nil {
		t.Fatal(err)
	}
	approved, err := p.Approve(now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SavePlan(t.Context(), approved, p.Version); err != nil {
		t.Fatal(err)
	}
	p, err = s.GetActivePlan(t.Context(), planTestProject)
	if err != nil {
		t.Fatal(err)
	}
	a.Stage = "completion"
	a.Binding.PlanID, a.Binding.PlanVersion, a.Binding.ApprovalScopeDigest = p.ID, p.Version, p.ApprovalScopeDigest
	a.Binding.RunID, a.Binding.RunVersion, a.Binding.Tree = "parent-run", 2, "checked-tree"
	admissions := []ProjectContextAdmission{}
	for _, task := range p.Tasks {
		item := a
		item.ID = "completion-context-" + task.ID
		item.Binding.TaskID = task.ID
		item.Binding.CanonicalTaskID = projectCompletionCanonicalTaskID(p.ID, p.Version, task.ID)
		item.Selection = projectcontext.Selection{TaskID: task.ID, CanonicalTaskID: item.Binding.CanonicalTaskID, Paths: task.Paths, Components: []string{"store"}}
		repacketContextAdmission(t, s, &item)
		admissions = append(admissions, item)
	}
	binding := verification.Binding{ProjectID: c.Project.CanonicalID, GoalID: a.Binding.GoalID, GoalRevision: a.Binding.GoalRevision, PlanID: p.ID, PlanVersion: p.Version, RunID: a.Binding.RunID, RunVersion: a.Binding.RunVersion, TreeDigest: a.Binding.Tree, EnvironmentDigest: "checked-environment"}
	session := verification.Session{ID: "verify-context-completion", Version: 1, Binding: binding, State: verification.VerifiedComplete, Criteria: []verification.Criterion{{ID: "result", Mandatory: true, ClaimIDs: []string{"claim"}}}, Claims: []verification.Claim{{ID: "claim", CriterionID: "result", Critical: true, SemanticScope: []string{"runtime"}, EvidenceIDs: []string{"independent-a", "independent-b"}}}, RequiredChecks: map[string]verification.Status{"checks": verification.StatusPass}, CreatedAt: now, UpdatedAt: now}
	for _, id := range []string{"independent-a", "independent-b"} {
		session.Evidence = append(session.Evidence, verification.Evidence{ID: id, ClaimID: "claim", Status: verification.StatusPass, ContentDigest: id, TreeDigest: binding.TreeDigest, EnvironmentDigest: binding.EnvironmentDigest, Producer: id, Provider: id, Oracle: id, ClusterID: id, Attempts: 1, Passes: 1})
	}
	if err = s.CreateVerificationSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	bundle, err := verification.BuildEvidenceBundle("completion-bundle", session.ID, binding, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	attestation, err := verification.NewCompletionAttestation("completion-parent", session, bundle.ManifestDigest, "authenticated-runtime-verification", now)
	if err != nil {
		t.Fatal(err)
	}
	compatibility := projectcontext.Receipt{ID: "compatibility-actual-review", Stage: "architecture_compatibility", Outcome: "COMPATIBLE", Binding: a.Binding, CurrentContract: a.Binding.Contract, CheckedAt: now, CompatibilityEvidenceDigest: projectcontext.Digest("actual-independent-review-input-result.v1", binding), PacketDigest: "reviewed-input", ReasonCode: "INDEPENDENT_CHECKED_REVIEW"}
	// Legacy bare-receipt fixture, deliberately missing canonical producer
	// evidence. It may support negative tests; it never qualifies completion.
	raw, _ := json.Marshal(compatibility)
	bound, _ := json.Marshal(compatibility.Binding)
	if _, err = s.db.ExecContext(t.Context(), `INSERT INTO extreme_project_context_receipts(receipt_id,project_id,contract_id,contract_revision,stage,manifest_digest,packet_digest,binding_json,receipt_json,outcome,required_count,included_count,rendered_bytes,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, compatibility.ID, c.Project.CanonicalID, c.ID, c.Revision, compatibility.Stage, "review-manifest", compatibility.PacketDigest, string(bound), string(raw), compatibility.Outcome, 0, 0, 0, now.Format(timeLayout)); err != nil {
		t.Fatal(err)
	}
	return s, admissions, verification.BundleEnvelope{Bundle: bundle, Payloads: map[string][]byte{}}, attestation, compatibility
}

func TestProjectContextCompletionNeedsCanonicalIndependentCompatibilityAndAllTasks(t *testing.T) {
	for _, scenario := range []string{"missing compatibility", "omit task", "duplicate task", "wrong tree", "wrong canonical task", "omit components", "omit paths"} {
		t.Run(scenario, func(t *testing.T) {
			s, admissions, envelope, attestation, compatible := projectCanonicalCompletionFixture(t)
			id := compatible.ID
			switch scenario {
			case "missing compatibility":
				id = "producer-claimed-pass"
			case "omit task":
				admissions = admissions[:1]
			case "duplicate task":
				admissions[1] = admissions[0]
			case "wrong tree":
				admissions[0].Binding.Tree = "producer-tree"
			case "wrong canonical task":
				admissions[0].Selection.CanonicalTaskID = "producer-task"
				admissions[0].Binding.CanonicalTaskID = "producer-task"
			case "omit components":
				admissions[0].Selection.Components = nil
			case "omit paths":
				admissions[0].Selection.Paths = nil
			}
			if _, err := s.CommitProjectContextCompletion(t.Context(), admissions, envelope, attestation, id); err == nil {
				t.Fatal("incomplete or producer-only compatibility became parent completion")
			}
			var n int
			if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM completion_attestations`).Scan(&n); err != nil || n != 0 {
				t.Fatal("refusal left completion attestation", n, err)
			}
		})
	}
	s, admissions, envelope, attestation, compatible := projectCanonicalCompletionFixture(t)
	receipts, err := s.CommitProjectContextCompletion(t.Context(), admissions, envelope, attestation, compatible.ID)
	if err != nil || len(receipts) != 2 {
		t.Fatal("exact independently reviewed aggregate refused", receipts, err)
	}
	if receipts[0].IncludedCount != 1 || receipts[1].IncludedCount != 2 {
		t.Fatal("task-only required policy was missing or leaked to another task", receipts)
	}
	persisted, err := s.LatestCompletionAttestation(t.Context(), attestation.VerificationID)
	if err != nil || persisted.Digest != attestation.Digest {
		t.Fatal("completion coverage committed without exact attestation", err)
	}
	if _, err = s.ReadProjectContextReceipt(t.Context(), "missing"); !errors.Is(err, projectcontext.ErrMissing) {
		t.Fatal("missing receipt treated as qualification", err)
	}
}

func TestProjectContextCompletionMutationBetweenPrepareAndCommitRollsBackAllTasks(t *testing.T) {
	s, admissions, envelope, attestation, compatible := projectCanonicalCompletionFixture(t)
	ready, changed := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(changed)
		<-ready
		_, err := s.UpdateMemory(t.Context(), "PROJECT-local", "MEM-inspect-task-only", 1, func(r *model.MemoryRecordV2) error { r.ACLScope = "revoked-owner"; return nil })
		if err != nil {
			t.Error(err)
		}
	}()
	close(ready)
	<-changed
	if _, err := s.CommitProjectContextCompletion(t.Context(), admissions, envelope, attestation, compatible.ID); err == nil {
		t.Fatal("task-only required policy changed after prepare but parent completed")
	}
	for _, query := range []string{`SELECT count(*) FROM completion_attestations`, `SELECT count(*) FROM extreme_project_context_receipts WHERE stage='completion'`, `SELECT count(*) FROM audit_events WHERE event_id LIKE 'context-completion-%'`} {
		var n int
		if err := s.db.QueryRowContext(t.Context(), query).Scan(&n); err != nil || n != 0 {
			t.Fatal("failed aggregate completion left canonical success/partial coverage", n, err)
		}
	}
}

func TestProjectContextCompletionAttestationInsertFaultRollsBackCoverageAndAudit(t *testing.T) {
	s, admissions, envelope, attestation, compatible := projectCanonicalCompletionFixture(t)
	if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER context_completion_fault BEFORE INSERT ON completion_attestations BEGIN SELECT RAISE(ABORT,'completion fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitProjectContextCompletion(t.Context(), admissions, envelope, attestation, compatible.ID); err == nil {
		t.Fatal("attestation fault committed task context receipts")
	}
	var n int
	if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM extreme_project_context_receipts WHERE stage='completion'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial completion receipt persisted", n, err)
	}
	if _, err := s.db.ExecContext(t.Context(), `DROP TRIGGER context_completion_fault`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitProjectContextCompletion(t.Context(), admissions, envelope, attestation, compatible.ID); err != nil {
		t.Fatal("exact safe retry after rollback refused", err)
	}
}

func TestProjectContextCompletionRejectsNonPassCanonicalReviewAndVerifierDrift(t *testing.T) {
	for _, outcome := range []string{"INCOMPATIBLE", "UNKNOWN", "NOT_APPLICABLE", "QUALIFIED"} {
		t.Run(outcome, func(t *testing.T) {
			s, admissions, envelope, attestation, compatible := projectCanonicalCompletionFixture(t)
			compatible.ID, compatible.Outcome = "compatibility-"+outcome, outcome
			raw, _ := json.Marshal(compatible)
			bound, _ := json.Marshal(compatible.Binding)
			if _, err := s.db.ExecContext(t.Context(), `INSERT INTO extreme_project_context_receipts(receipt_id,project_id,contract_id,contract_revision,stage,manifest_digest,packet_digest,binding_json,receipt_json,outcome,required_count,included_count,rendered_bytes,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, compatible.ID, compatible.Binding.Project.CanonicalID, compatible.CurrentContract.ID, compatible.CurrentContract.Revision, compatible.Stage, "review-manifest", compatible.PacketDigest, string(bound), string(raw), compatible.Outcome, 0, 0, 0, compatible.CheckedAt.Format(timeLayout)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CommitProjectContextCompletion(t.Context(), admissions, envelope, attestation, compatible.ID); !errors.Is(err, projectcontext.ErrChanged) {
				t.Fatal("non-PASS review became architecture compatibility", err)
			}
		})
	}
	for _, scenario := range []string{"verification version", "canonical failed evidence", "bundle payload", "attestation tamper", "foreign environment"} {
		t.Run(scenario, func(t *testing.T) {
			s, admissions, envelope, attestation, compatible := projectCanonicalCompletionFixture(t)
			session, err := s.GetVerificationSession(t.Context(), attestation.VerificationID)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "verification version":
				session.Version++
				if err = s.UpdateVerificationSession(t.Context(), session, session.Version-1); err != nil {
					t.Fatal(err)
				}
			case "canonical failed evidence":
				session.Version++
				session.Evidence[0].Status = verification.StatusFail
				if err = s.UpdateVerificationSession(t.Context(), session, session.Version-1); err != nil {
					t.Fatal(err)
				}
				attestation, err = verification.NewCompletionAttestation(attestation.ID, session, envelope.Bundle.ManifestDigest, attestation.Provenance, admissions[0].Now)
				if err != nil {
					t.Fatal(err)
				}
			case "bundle payload":
				envelope.Payloads["producer-assertion"] = []byte("pass")
			case "attestation tamper":
				attestation.Provenance = "producer-assertion"
			case "foreign environment":
				attestation.Binding.EnvironmentDigest = "another-environment"
			}
			if _, err = s.CommitProjectContextCompletion(t.Context(), admissions, envelope, attestation, compatible.ID); err == nil {
				t.Fatal("drifted verifier evidence became completion")
			}
			var n int
			if err = s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM completion_attestations`).Scan(&n); err != nil || n != 0 {
				t.Fatal("failed verification left canonical attestation", n, err)
			}
		})
	}
}
