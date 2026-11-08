//go:build !governed

package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/verification"
)

// All invocation metadata below is a store fixture. It proves no native model,
// paid invocation, protected approval, or runtime successful-turn token.
func architectureFixture(t *testing.T, futureTaskMemory ...bool) (*Store, ProjectContextAdmission, ProjectArchitectureObservation, verification.BundleEnvelope, []ProjectContextAdmission) {
	t.Helper()
	s, tasks, _, oldAttestation, _ := projectCompletionFixture(t)
	a := tasks[0]
	accepted, err := s.ReadAcceptedProjectContract(t.Context(), a.Binding.Project)
	if err != nil {
		t.Fatal(err)
	}
	if len(futureTaskMemory) > 0 && futureTaskMemory[0] {
		contract := accepted.Contract
		contract.Revision++
		contract.RevisionReason = "Fixture reviewed mandatory future-task memory"
		contract.SupersedesDigest = accepted.Digest
		for i := range contract.Required {
			if contract.Required[i].ID == "MEM-inspect-task-only" {
				contract.Required[i].Applies = projectcontext.Applicability{TaskIDs: []string{"future-task"}}
			}
		}
		_, contract.ReviewedInventoryDigest, err = s.ProjectContractInventory(t.Context(), contract.Project, a.Audience)
		if err != nil {
			t.Fatal(err)
		}
		request := admitContextDecision(t, s, contract, true, "future-task-architecture")
		contract, err = s.CommitMCPProjectContractDecision(t.Context(), request, true, a.Audience, a.Now, contextAudit(request, a.Now))
		if err != nil {
			t.Fatal(err)
		}
		accepted, err = s.ReadAcceptedProjectContract(t.Context(), contract.Project)
		if err != nil {
			t.Fatal(err)
		}
		a.Binding.Contract = projectcontext.ContractRef{ID: contract.ID, Revision: contract.Revision, Digest: accepted.Digest}
		for i := range tasks {
			tasks[i].Binding.Contract = a.Binding.Contract
		}
	}
	p, err := s.GetActivePlan(t.Context(), planTestProject)
	if err != nil {
		t.Fatal(err)
	}
	revised, err := p.Revise(p.Version, "Fixture reviewed required architecture pin", a.Now)
	if err != nil {
		t.Fatal(err)
	}
	revised.State = plan.StateReady // Fixture draft's completed readiness evaluation.
	revised.RequiredProjectContract = &a.Binding.Contract
	if err = s.SavePlan(t.Context(), revised, p.Version); err != nil {
		t.Fatal(err)
	}
	approved, err := revised.Approve(a.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SavePlan(t.Context(), approved, revised.Version); err != nil {
		t.Fatal(err)
	}
	approved, err = s.GetActivePlan(t.Context(), planTestProject)
	if err != nil {
		t.Fatal(err)
	}
	a.ID, a.Stage = "architecture-review-context", "review"
	a.Binding.PlanVersion, a.Binding.ApprovalScopeDigest = approved.Version, approved.ApprovalScopeDigest
	a.Binding.Tree = strings.Repeat("a", 64)
	a.Binding.TaskID, a.Binding.CanonicalTaskID, a.Binding.Provider, a.Binding.Model, a.Binding.Attempt = "", "", "", "", 0
	a.Selection, err = projectcontext.PlanningSelection(accepted.Contract)
	if err != nil {
		t.Fatal(err)
	}
	repacketContextAdmission(t, s, &a)
	authority, err := s.ReadProjectContextAuthority(t.Context(), ProjectContextAuthorityRequest{Binding: a.Binding, Purpose: ProjectContextAuthorityCompletion}, a.Now)
	if err != nil {
		t.Fatal(err)
	}
	a.ExpectedAuthority = &authority
	session, err := s.GetVerificationSession(t.Context(), oldAttestation.VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	session.Version++
	session.Binding.PlanVersion, session.Binding.TreeDigest = approved.Version, a.Binding.Tree
	for i := range session.Evidence {
		session.Evidence[i].TreeDigest = a.Binding.Tree
	}
	if err = s.UpdateVerificationSession(t.Context(), session, session.Version-1); err != nil {
		t.Fatal(err)
	}
	bundle, err := verification.BuildEvidenceBundle("architecture-bundle", session.ID, session.Binding, nil, a.Now)
	if err != nil {
		t.Fatal(err)
	}
	envelope := verification.BundleEnvelope{Bundle: bundle, Payloads: map[string][]byte{}}
	input, err := s.ReadProjectArchitectureReview(t.Context(), a, session.ID, session.Version, envelope)
	if err != nil {
		t.Fatal("complete canonical architecture input refused", err)
	}
	receipt, err := s.CommitProjectContextAdmission(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	a.Binding = receipt.Binding
	observation := ProjectArchitectureObservation{ID: "architecture-compatibility-fixture", VerificationID: session.ID, VerificationVersion: session.Version, InputDigest: input.Digest,
		Invocation: ProjectArchitectureInvocation{ID: "fixture-invocation", Provider: "claude", ObservedModel: "fixture-observed-model", SessionID: "fixture-fresh-session", BinaryDigest: projectcontext.Digest("fixture-binary", 1), InputDigest: projectcontext.Digest("fixture-prompt", 1), OutputDigest: projectcontext.Digest("fixture-output", 1), PayloadDigest: projectcontext.Digest("fixture-payload", 1), SourceHead: strings.Repeat("b", 40), WorkspaceTree: a.Binding.Tree, ContextReceiptID: receipt.ID, StartedAt: a.Now, FinishedAt: a.Now}}
	for _, id := range input.Obligations {
		observation.Assessments = append(observation.Assessments, ProjectArchitectureAssessment{ObligationID: id, Status: "PASS", Reason: "Fixture architecture evidence supports this obligation", EvidenceRefs: []string{"obligation:" + id, "workspace:" + a.Binding.Tree}})
	}
	for i := range tasks {
		tasks[i].Binding.PlanVersion, tasks[i].Binding.ApprovalScopeDigest, tasks[i].Binding.Tree = approved.Version, approved.ApprovalScopeDigest, a.Binding.Tree
		tasks[i].Binding.CanonicalTaskID = projectCompletionCanonicalTaskID(approved.ID, approved.Version, tasks[i].Binding.TaskID)
		tasks[i].Selection.CanonicalTaskID = tasks[i].Binding.CanonicalTaskID
		repacketContextAdmission(t, s, &tasks[i])
	}
	return s, a, observation, envelope, tasks
}

func TestProjectArchitectureCompatibilityCanonicalFullCoverageAndStrictCompletion(t *testing.T) {
	s, a, o, envelope, tasks := architectureFixture(t)
	input, err := s.ReadProjectArchitectureReview(t.Context(), a, o.VerificationID, o.VerificationVersion, envelope)
	if err != nil || len(input.Packet.Loaded) != 2 || !strings.Contains(input.Packet.Text, "MEM-inspect-task-only") {
		t.Fatal("task-only required architecture was omitted", input, err)
	}
	before := queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`)
	receipt, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, o, envelope)
	if err != nil || receipt.Outcome != "COMPATIBLE" || receipt.Stage != "architecture_compatibility" || receipt.RequiredCount != 2 || receipt.IncludedCount != 2 || receipt.Binding.RunID != a.Binding.RunID {
		t.Fatal("whole verified independent architecture fixture refused", receipt, err)
	}
	if queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`) != before+1 {
		t.Fatal("unexpected extra qualification receipts")
	}
	persisted, err := s.ReadProjectContextReceipt(t.Context(), receipt.ID)
	if err != nil || persisted.CompatibilityEvidenceDigest != receipt.CompatibilityEvidenceDigest {
		t.Fatal(persisted, err)
	}
	if _, err = s.db.ExecContext(t.Context(), `UPDATE extreme_project_context_receipts SET outcome='FORGED' WHERE receipt_id=?`, receipt.ID); err == nil {
		t.Fatal("compatibility receipt mutable")
	}
	session, err := s.GetVerificationSession(t.Context(), o.VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	attestation, err := verification.NewCompletionAttestation("architecture-qualified-completion", session, envelope.Bundle.ManifestDigest, "fixture-runtime-completion", a.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitProjectContextCompletion(t.Context(), tasks, envelope, attestation, receipt.ID); err != nil {
		t.Fatal("canonical architecture receipt did not qualify strict all-task completion", err)
	}
}

func TestProjectArchitectureCompatibilityRefusesPartialOrClaimedProof(t *testing.T) {
	for _, scenario := range []string{"omit obligation", "duplicate obligation", "foreign obligation", "unresolved evidence", "no concrete evidence", "missing receipt", "blank parent", "wrong tree", "bad output digest", "resumed session", "bad time", "foreign input", "foreign verification", "missing authority", "missing planning coverage"} {
		t.Run(scenario, func(t *testing.T) {
			s, a, o, envelope, _ := architectureFixture(t)
			switch scenario {
			case "omit obligation":
				o.Assessments = o.Assessments[:len(o.Assessments)-1]
			case "duplicate obligation":
				o.Assessments[1] = o.Assessments[0]
			case "foreign obligation":
				o.Assessments[0].ObligationID = "caller-pass"
			case "unresolved evidence":
				o.Assessments[0].EvidenceRefs = []string{"file:invented"}
			case "no concrete evidence":
				o.Assessments[0].EvidenceRefs = []string{"obligation:" + o.Assessments[0].ObligationID}
			case "missing receipt":
				o.Invocation.ContextReceiptID = "caller-qualified"
			case "blank parent":
				a.Binding.RunID = ""
			case "wrong tree":
				o.Invocation.WorkspaceTree = strings.Repeat("c", 64)
			case "bad output digest":
				o.Invocation.OutputDigest = "PASS"
			case "resumed session":
				o.Invocation.PriorSessionID = o.Invocation.SessionID
			case "bad time":
				o.Invocation.FinishedAt = a.Now.Add(time.Second)
			case "foreign input":
				o.InputDigest = projectcontext.Digest("caller", true)
			case "foreign verification":
				o.VerificationVersion++
			case "missing authority":
				a.ExpectedAuthority = nil
			case "missing planning coverage":
				a.Selection = projectcontext.Selection{}
			}
			before := queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`)
			if _, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, o, envelope); err == nil {
				t.Fatal("caller-shaped or partial evidence qualified architecture")
			}
			if queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`) != before || queryInt(t, s.db, `SELECT count(*) FROM audit_events WHERE event_type='PROJECT_ARCHITECTURE_COMPATIBILITY_RECORDED'`) != 0 {
				t.Fatal("refusal left compatibility success/evidence")
			}
		})
	}
}

func TestProjectArchitectureCompatibilityNonPassAndUnknownIdentityCannotComplete(t *testing.T) {
	for _, scenario := range []string{"FAIL", "UNKNOWN", "N/A", "missing model", "missing session", "unknown model", "unknown session"} {
		t.Run(scenario, func(t *testing.T) {
			s, a, o, envelope, tasks := architectureFixture(t)
			switch scenario {
			case "missing model":
				o.Invocation.ObservedModel = ""
			case "missing session":
				o.Invocation.SessionID = ""
			case "unknown model":
				o.Invocation.ObservedModel = " UNKNOWN "
			case "unknown session":
				o.Invocation.SessionID = " UNKNOWN "
			default:
				o.Assessments[0].Status = scenario
			}
			receipt, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, o, envelope)
			if err != nil || receipt.Outcome == "COMPATIBLE" {
				t.Fatal("non-pass or unidentified review certified architecture", receipt, err)
			}
			session, _ := s.GetVerificationSession(t.Context(), o.VerificationID)
			attestation, _ := verification.NewCompletionAttestation("non-pass-completion", session, envelope.Bundle.ManifestDigest, "fixture", a.Now)
			if _, err = s.CommitProjectContextCompletion(t.Context(), tasks, envelope, attestation, receipt.ID); err == nil {
				t.Fatal("non-pass architecture completed project")
			}
		})
	}
}

func TestProjectArchitectureCompatibilityCurrentTransactionAndRollback(t *testing.T) {
	for _, scenario := range []string{"ACL revoke", "new unknown memory", "verification update", "plan update", "audit fault"} {
		t.Run(scenario, func(t *testing.T) {
			s, a, o, envelope, _ := architectureFixture(t)
			switch scenario {
			case "ACL revoke":
				_, err := s.UpdateMemory(t.Context(), a.Binding.Project.StorageID, "MEM-inspect-task-only", 1, func(r *model.MemoryRecordV2) error { r.ACLScope = "revoked-owner"; return nil })
				if err != nil {
					t.Fatal(err)
				}
			case "new unknown memory":
				r := contextStoreRecord("MEM-new-architecture", a.Now)
				if err := s.WriteMemoryV2(t.Context(), r); err != nil {
					t.Fatal(err)
				}
			case "verification update":
				v, _ := s.GetVerificationSession(t.Context(), o.VerificationID)
				v.Version++
				if err := s.UpdateVerificationSession(t.Context(), v, v.Version-1); err != nil {
					t.Fatal(err)
				}
			case "plan update":
				p, _ := s.GetActivePlan(t.Context(), planTestProject)
				r, err := p.Revise(p.Version, "Changed architecture review basis", a.Now)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.SavePlan(t.Context(), r, p.Version); err != nil {
					t.Fatal(err)
				}
			case "audit fault":
				if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER architecture_audit_fault BEFORE INSERT ON audit_events WHEN NEW.event_type='PROJECT_ARCHITECTURE_COMPATIBILITY_RECORDED' BEGIN SELECT RAISE(ABORT,'architecture audit fault'); END`); err != nil {
					t.Fatal(err)
				}
			}
			before := queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`)
			if _, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, o, envelope); err == nil {
				t.Fatal("stale or faulted review persisted")
			}
			if queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`) != before {
				t.Fatal("partial architecture receipt survived rollback")
			}
			if scenario == "audit fault" {
				if _, err := s.db.ExecContext(t.Context(), `DROP TRIGGER architecture_audit_fault`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, o, envelope); err != nil {
					t.Fatal("safe retry refused", err)
				}
			}
		})
	}
}

func TestProjectArchitectureInspectionIsReadOnlyAndDoesNotLeakPastACL(t *testing.T) {
	s, a, o, envelope, _ := architectureFixture(t)
	before := queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`)
	input, err := s.ReadProjectArchitectureReview(t.Context(), a, o.VerificationID, o.VerificationVersion, envelope)
	if err != nil || input.Digest == "" {
		t.Fatal(err)
	}
	if queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`) != before {
		t.Fatal("inspection granted compatibility")
	}
	a.Audience.ScopeIDs = []string{a.Binding.Project.StorageID}
	denied, err := s.ReadProjectArchitectureReview(t.Context(), a, o.VerificationID, o.VerificationVersion, envelope)
	raw, _ := json.Marshal(denied)
	if err == nil || strings.Contains(string(raw), "MEM-inspect-task-only") {
		t.Fatal("hidden required memory leaked as partial input", denied, err)
	}
}

func TestProjectArchitectureInvocationReplayAndEvidencePrivacy(t *testing.T) {
	for _, scenario := range []string{"invocation replay", "session replay"} {
		t.Run(scenario, func(t *testing.T) {
			s, a, o, envelope, _ := architectureFixture(t)
			if _, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, o, envelope); err != nil {
				t.Fatal(err)
			}
			o.ID = "second-architecture-receipt"
			if scenario == "session replay" {
				o.Invocation.ID = "different-invocation"
			} else {
				o.Invocation.SessionID = "different-session"
			}
			if _, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, o, envelope); err == nil {
				t.Fatal("reused actual invocation/session metadata made another compatibility receipt")
			}
			var raw string
			if err := s.db.QueryRowContext(t.Context(), `SELECT data_json FROM audit_events WHERE event_type='PROJECT_ARCHITECTURE_COMPATIBILITY_RECORDED'`).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var record projectArchitectureEvidenceRecord
			if err := json.Unmarshal([]byte(raw), &record); err != nil || record.Input.Packet.Text != "" {
				t.Fatal("architecture evidence retained a duplicate memory prompt", err)
			}
			if queryInt(t, s.db, `SELECT count(*) FROM audit_events WHERE event_type='PROJECT_ARCHITECTURE_COMPATIBILITY_RECORDED'`) != 1 {
				t.Fatal("replay left extra evidence")
			}
		})
	}
}

func TestProjectArchitectureLateAuditFailureRollsBackReceiptAndAllEvidence(t *testing.T) {
	s, a, o, envelope, _ := architectureFixture(t)
	if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER architecture_session_fault BEFORE INSERT ON audit_events WHEN NEW.event_type='PROJECT_ARCHITECTURE_SESSION_BOUND' BEGIN SELECT RAISE(ABORT,'late architecture session fault'); END`); err != nil {
		t.Fatal(err)
	}
	before := queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`)
	if _, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, o, envelope); err == nil {
		t.Fatal("late session identity audit fault succeeded")
	}
	if queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`) != before || queryInt(t, s.db, `SELECT count(*) FROM audit_events WHERE event_type LIKE 'PROJECT_ARCHITECTURE_%'`) != 0 {
		t.Fatal("late failure left partial architecture coverage/provenance")
	}
	if _, err := s.db.ExecContext(t.Context(), `DROP TRIGGER architecture_session_fault`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, o, envelope); err != nil {
		t.Fatal("safe exact retry after rollback refused", err)
	}
}
