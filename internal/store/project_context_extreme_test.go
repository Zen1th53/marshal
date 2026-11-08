//go:build !governed

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func contextStoreRecord(id string, now time.Time) model.MemoryRecordV2 {
	r := model.MemoryRecordV2{ID: id, ProjectID: "PROJECT-local", Revision: 1, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityOperator, Confidence: model.ConfidenceVerified, Title: "Preserve accepted history", Body: "Keep original accepted decisions and evidence.", Scope: "project", ScopeID: "PROJECT-local", ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime_service", Reference: "owner"}}
	r.ContentDigest = r.CanonicalDigest()
	return r
}
func contextStoreDraft(t *testing.T, s *Store, records []model.MemoryRecordV2) (projectcontext.Contract, ProjectContextAudience, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	a := ProjectContextAudience{Actor: "owner", ScopeIDs: []string{"PROJECT-local"}}
	project := projectcontext.ProjectBinding{CanonicalID: string(planTestProject), StorageID: "PROJECT-local"}
	for _, r := range records {
		found := false
		for _, scope := range a.ScopeIDs {
			if scope == r.ScopeID {
				found = true
			}
		}
		if !found {
			a.ScopeIDs = append(a.ScopeIDs, r.ScopeID)
		}
	}
	_, inventory, err := s.ProjectContractInventory(t.Context(), project, a)
	if err != nil {
		t.Fatal(err)
	}
	c := projectcontext.Contract{FormatVersion: 1, Project: project, ID: "PROJECT-CONTRACT", Revision: 1, Purpose: "Govern accepted project work", Components: []projectcontext.Component{{ID: "store", Responsibility: "Preserve canonical state", Paths: []string{"internal/store"}}}, Invariants: []string{"Preserve original history"}, ReviewedInventoryDigest: inventory}
	for _, r := range records {
		c.Required = append(c.Required, projectcontext.MemoryRef{ID: r.ID, Scope: r.Scope, ScopeID: r.ScopeID, ProjectID: r.ProjectID, Revision: r.Revision, ContentDigest: r.ContentDigest, StateFingerprint: projectcontext.MemoryFingerprint(r), Class: projectcontext.Policy, Applies: projectcontext.Applicability{ProjectWide: true}, AcceptanceOccurrence: "supported-policy-source", Reason: "Required preservation policy"})
	}
	return c, a, now
}
func admitContextDecision(t *testing.T, s *Store, c projectcontext.Contract, allow bool, suffix string) permission.Request {
	t.Helper()
	source, _, err := s.AdmitMCPProjectContract(t.Context(), MCPProposal{ID: "mcp:contract-" + suffix, Launch: "launch", Owner: "owner", Provider: "codex", Submission: "contract-" + suffix}, ProjectContractSubmission{Action: "project-contract", ExpectedVersion: c.Revision - 1, Contract: c})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := c.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := json.Marshal(ProjectContractApprovalBinding{source.Digest, digest, c.ReviewedInventoryDigest, c.Revision - 1})
	req := permission.Request{Kind: "project-contract", Object: c.ID, Scope: c.Project.StorageID, Who: "Marshal", Reason: "Reviewed project contract", ProposalID: source.ID, ApprovalBinding: string(binding), SourceLaunch: "launch", SourceOccurrence: "mcp:decision-" + suffix}
	raw, _ := json.Marshal(map[string]any{"action": "decide", "id": "QUESTION-" + suffix, "allow": allow, "reason": "Reviewed the original exact contract"})
	_, _, err = s.AdmitMCPProposal(t.Context(), req.Scope, MCPProposal{ID: req.SourceOccurrence, Launch: "launch", Owner: "owner", Provider: "codex", Submission: "decision-" + suffix, Payload: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	requestJSON, _ := json.Marshal(req)
	if err = s.AcceptMCPDecision(t.Context(), req.Scope, req.SourceOccurrence, string(requestJSON), req.Key()); err != nil {
		t.Fatal(err)
	}
	if err = s.SetMCPProposalState(t.Context(), req.Scope, req.SourceOccurrence, "applying", "protected test effect entered"); err != nil {
		t.Fatal(err)
	}
	return req
}
func contextAudit(req permission.Request, now time.Time) model.Event {
	return model.Event{ID: "accepted-" + req.SourceOccurrence, ProjectID: req.Scope, Type: "PERMISSION_DECIDED", Timestamp: now, Data: map[string]any{"source_occurrence": req.SourceOccurrence}}
}
func acceptedContextStore(t *testing.T) (*Store, projectcontext.Contract, ProjectContextAudience, time.Time) {
	t.Helper()
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	r := contextStoreRecord("MEM-required", now)
	if err := s.WriteMemoryV2(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	c, a, now := contextStoreDraft(t, s, []model.MemoryRecordV2{r})
	req := admitContextDecision(t, s, c, true, "first")
	accepted, err := s.CommitMCPProjectContractDecision(t.Context(), req, true, a, now, contextAudit(req, now))
	if err != nil {
		t.Fatal(err)
	}
	return s, accepted, a, now
}

func TestProjectContextContractNeedsSeparateJournalDecisionAndCanonicalLifecycle(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	r := contextStoreRecord("MEM-candidate", now)
	r.Lifecycle = model.MemoryCandidate
	r.Authority = model.AuthorityAgent
	r.ContentDigest = r.CanonicalDigest()
	if err := s.WriteMemoryV2(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	c, a, now := contextStoreDraft(t, s, []model.MemoryRecordV2{r})
	req := admitContextDecision(t, s, c, true, "candidate")
	worker := a
	worker.Worker = true
	if _, err := s.CommitMCPProjectContractDecision(t.Context(), req, true, worker, now, contextAudit(req, now)); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal("worker self-authorized a contract decision", err)
	}
	if _, _, err := s.ProjectContractInventory(t.Context(), c.Project, worker); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal("worker obtained trusted full inventory", err)
	}
	if _, err := s.CommitMCPProjectContractDecision(t.Context(), req, true, a, now, contextAudit(req, now)); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatal("contract class promoted retained candidate", err)
	}
	for _, bad := range []permission.Request{{Kind: "project-contract", Scope: req.Scope, ProposalID: req.ProposalID, SourceOccurrence: req.ProposalID, SourceLaunch: req.SourceLaunch}, {Kind: "memory", Scope: req.Scope, ProposalID: req.ProposalID, SourceOccurrence: req.SourceOccurrence, SourceLaunch: req.SourceLaunch}} {
		if _, err := s.CommitMCPProjectContractDecision(t.Context(), bad, true, a, now, contextAudit(req, now)); err == nil {
			t.Fatal("raw/candidate authority activated contract")
		}
	}
	stored, err := s.GetMemoryV2(t.Context(), r.ProjectID, r.ID)
	if err != nil || stored.Lifecycle != model.MemoryCandidate || stored.Authority != model.AuthorityAgent {
		t.Fatal("candidate was rewritten", stored, err)
	}
	var n int
	if err = s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM extreme_project_contract_active").Scan(&n); err != nil || n != 0 {
		t.Fatal("failed decision activated project", n, err)
	}
}

func TestProjectContractSubmissionPreserves18AcceptedReferencesWithinBoundedDraft(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	var records []model.MemoryRecordV2
	for i := 0; i < 18; i++ {
		r := contextStoreRecord(fmt.Sprintf("MEM-required-%02d", i), now)
		if err := s.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	c, audience, now := contextStoreDraft(t, s, records)
	raw, _ := json.Marshal(ProjectContractSubmission{Action: "project-contract", Contract: c})
	if len(raw) <= 4096 || len(raw) > 64<<10 {
		t.Fatalf("fixture must exceed inspection response cap: %d", len(raw))
	}
	req := admitContextDecision(t, s, c, true, "eighteen")
	accepted, err := s.CommitMCPProjectContractDecision(t.Context(), req, true, audience, now, contextAudit(req, now))
	if err != nil || len(accepted.Required) != len(records) {
		t.Fatalf("accepted references omitted: %d, %v", len(accepted.Required), err)
	}
	oversized := c
	oversized.Purpose = strings.Repeat("x", 64<<10)
	if _, _, err = s.AdmitMCPProjectContract(t.Context(), MCPProposal{ID: "oversized", Launch: "launch", Owner: "owner", Provider: "codex", Submission: "oversized"}, ProjectContractSubmission{Action: "project-contract", Contract: oversized}); !errors.Is(err, projectcontext.ErrBudget) {
		t.Fatalf("unbounded draft admitted: %v", err)
	}
	if _, err = s.MCPProposal(t.Context(), c.Project.StorageID, "oversized"); err == nil {
		t.Fatal("oversized draft reached durable journal")
	}
}
func TestProjectContextContractAtomicAcceptanceDenialCASAndImmutableHistory(t *testing.T) {
	s, c, a, now := acceptedContextStore(t)
	snapshot, err := s.ReadProjectContextSnapshot(t.Context(), c.Project, a, projectcontext.Selection{TaskID: "implement"}, now)
	if err != nil || snapshot.Version != 1 || len(snapshot.Records) != 1 {
		t.Fatal(snapshot, err)
	}
	for _, q := range []string{"UPDATE extreme_project_contract_revisions SET digest='forged'", "DELETE FROM extreme_project_contract_revisions", "UPDATE extreme_project_contract_required_refs SET class='context_only'", "DELETE FROM extreme_project_contract_required_refs"} {
		if _, err = s.db.ExecContext(t.Context(), q); err == nil {
			t.Fatal("immutable accepted history changed", q)
		}
	}
	req := admitContextDecision(t, s, c, true, "stale")
	if _, err = s.CommitMCPProjectContractDecision(t.Context(), req, true, a, now, contextAudit(req, now)); !errors.Is(err, model.ErrConflict) {
		t.Fatal("stale CAS accepted", err)
	}
	c.Revision = 2
	c.RevisionReason = "Retain exact accepted policy"
	c.SupersedesDigest = snapshot.ContractDigest
	req = admitContextDecision(t, s, c, false, "deny")
	if _, err = s.CommitMCPProjectContractDecision(t.Context(), req, false, a, now, contextAudit(req, now)); err != nil {
		t.Fatal(err)
	}
	denied, err := s.MCPProposal(t.Context(), req.Scope, req.ProposalID)
	if err != nil || denied.State != "denied" {
		t.Fatal("deny receipt missing", denied, err)
	}
	unchanged, err := s.ReadProjectContextSnapshot(t.Context(), c.Project, a, projectcontext.Selection{TaskID: "implement"}, now)
	if err != nil || unchanged.Version != 1 {
		t.Fatal("deny moved accepted pointer", unchanged, err)
	}
}
func TestProjectContextContractFailureRollsBackCanonicalAndReceipt(t *testing.T) {
	for _, fault := range []string{
		`CREATE TRIGGER context_fault BEFORE INSERT ON extreme_project_contract_revisions BEGIN SELECT RAISE(ABORT,'revision fault'); END`,
		`CREATE TRIGGER context_fault BEFORE INSERT ON extreme_project_contract_required_refs BEGIN SELECT RAISE(ABORT,'ref fault'); END`,
		`CREATE TRIGGER context_fault BEFORE INSERT ON extreme_project_contract_active BEGIN SELECT RAISE(ABORT,'active fault'); END`,
		`CREATE TRIGGER context_fault BEFORE INSERT ON audit_events WHEN NEW.event_type='PERMISSION_DECIDED' BEGIN SELECT RAISE(ABORT,'audit fault'); END`,
		`CREATE TRIGGER context_fault BEFORE UPDATE ON extreme_mcp_proposals WHEN NEW.state='committed' BEGIN SELECT RAISE(ABORT,'receipt fault'); END`,
	} {
		t.Run(fault[45:55], func(t *testing.T) {
			s := projectStore(t)
			if err := s.InitMCPProposals(t.Context()); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
			r := contextStoreRecord("MEM-required", now)
			if err := s.WriteMemoryV2(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			c, a, now := contextStoreDraft(t, s, []model.MemoryRecordV2{r})
			req := admitContextDecision(t, s, c, true, "fault")
			if _, err := s.db.ExecContext(t.Context(), fault); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CommitMCPProjectContractDecision(t.Context(), req, true, a, now, contextAudit(req, now)); err == nil {
				t.Fatal("injected failure claimed acceptance")
			}
			for _, table := range []string{"extreme_project_contract_revisions", "extreme_project_contract_active", "extreme_project_contract_required_refs"} {
				var n int
				if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
					t.Fatal("partial accepted contract", table, n, err)
				}
			}
			p, err := s.MCPProposal(t.Context(), req.Scope, req.ProposalID)
			if err != nil || p.State != "admitted" {
				t.Fatal("canonical failure forged receipt", p, err)
			}
			if _, err = s.db.ExecContext(t.Context(), "DROP TRIGGER context_fault"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.CommitMCPProjectContractDecision(t.Context(), req, true, a, now, contextAudit(req, now)); err != nil {
				t.Fatal("safe exact retry failed", err)
			}
		})
	}
}
func contextAdmission(t *testing.T, s *Store, c projectcontext.Contract, a ProjectContextAudience, now time.Time) ProjectContextAdmission {
	t.Helper()
	goal := model.GoalContract{ID: "GOAL-1", SessionID: "context-session", Revision: 1, ProjectID: c.Project.CanonicalID, DesiredOutcome: "Finish the actual task", ExpectedArtifact: "verified result", Scope: []string{"internal/store"}, Constraints: []model.Constraint{{ID: "keep", Text: "Keep accepted facts", Source: "operator", IsHard: true, Scope: "global"}}, DoNotDo: []string{"Do not forget required policy"}, SuccessCriteria: []string{"Accepted work conforms to architecture"}, Risk: model.R1, AuthoritySource: "operator", BudgetRef: "budget", UnderstandingState: model.GoalReady, CreatedAt: now, UpdatedAt: now}
	if err := s.SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatal(err)
	}
	// Admission must render the persisted canonical Goal, including original text.
	goal, err := s.GetGoalContract(t.Context(), goal.ID, goal.Revision)
	if err != nil {
		t.Fatal(err)
	}
	selection := projectcontext.Selection{TaskID: "implement", Paths: []string{"internal/store"}}
	snapshot, err := s.ReadProjectContextSnapshot(t.Context(), c.Project, a, selection, now)
	if err != nil {
		t.Fatal(err)
	}
	binding := projectcontext.Binding{FormatVersion: 1, RequiredStateDigest: snapshot.StateDigest, Project: c.Project, Contract: projectcontext.ContractRef{ID: c.ID, Revision: c.Revision, Digest: snapshot.ContractDigest}, GoalID: goal.ID, GoalRevision: goal.Revision, TaskID: "implement", Attempt: 1, Provider: "codex", Principal: a.Actor, Session: goal.SessionID}
	packet, err := projectcontext.Render(projectcontext.Manifest{Contract: c, Goal: projectcontext.GoalFromCanonical(goal), Binding: binding, Selection: selection, Records: snapshot.Records}, 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	return ProjectContextAdmission{ID: "coverage-first", Stage: "planning", Audience: a, Selection: selection, Binding: binding, ExpectedStateDigest: snapshot.StateDigest, ExpectedPacketDigest: packet.Digest, ByteCap: 16 << 10, Now: now}
}
func TestProjectContextAdmissionExactPacketAtomicCoverageAndImmutability(t *testing.T) {
	s, c, a, now := acceptedContextStore(t)
	admission := contextAdmission(t, s, c, a, now)
	receipt, err := s.CommitProjectContextAdmission(t.Context(), admission)
	if err != nil || receipt.RequiredCount != 1 || receipt.PacketDigest != admission.ExpectedPacketDigest {
		t.Fatal(receipt, err)
	}
	if _, err = s.db.ExecContext(t.Context(), "UPDATE extreme_project_context_receipts SET outcome='FORGED'"); err == nil {
		t.Fatal("coverage rewritten")
	}
	if _, err = s.db.ExecContext(t.Context(), "DELETE FROM extreme_project_context_receipts"); err == nil {
		t.Fatal("coverage deleted")
	}
	admission.ID = "coverage-tampered"
	admission.ExpectedPacketDigest = "caller-proof"
	if _, err = s.CommitProjectContextAdmission(t.Context(), admission); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatal("caller packet proof accepted", err)
	}
	admission.ExpectedPacketDigest = receipt.PacketDigest
	admission.ByteCap = receipt.RenderedBytes - 1
	if _, err = s.CommitProjectContextAdmission(t.Context(), admission); !errors.Is(err, projectcontext.ErrBudget) {
		t.Fatal("overflow ignored", err)
	}
}

func TestProjectContextAdmissionCannotSelectAnotherTaskOrReuseAnotherPrincipalParent(t *testing.T) {
	s, c, a, now := acceptedContextStore(t)
	admission := contextAdmission(t, s, c, a, now)
	for _, field := range []string{"task", "canonical_task"} {
		mismatched := admission
		mismatched.ID = "wrong-selection-" + field
		if field == "task" {
			mismatched.Selection.TaskID = "other-task"
		} else {
			mismatched.Selection.CanonicalTaskID = "other-canonical-task"
		}
		repacketContextAdmission(t, s, &mismatched)
		if _, err := s.CommitProjectContextAdmission(t.Context(), mismatched); !errors.Is(err, projectcontext.ErrChanged) {
			t.Fatal("task selection differed from durable admission binding", field, err)
		}
	}
	parent, err := s.CommitProjectContextAdmission(t.Context(), admission)
	if err != nil {
		t.Fatal(err)
	}
	other := admission
	other.ID = "foreign-principal-parent"
	other.ParentReceiptID = parent.ID
	other.Audience.Actor = "another-owner"
	other.Binding.Principal = other.Audience.Actor
	repacketContextAdmission(t, s, &other)
	if _, err = s.CommitProjectContextAdmission(t.Context(), other); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatal("another principal reused original coverage chain", err)
	}
}
func TestProjectContextAdmissionMutationBetweenResolveAndCommitRefuses(t *testing.T) {
	for name, mutate := range map[string]func(*model.MemoryRecordV2){"acl": func(r *model.MemoryRecordV2) { r.ACLScope = "other" }, "conflict": func(r *model.MemoryRecordV2) { r.ConflictIDs = []string{"conflicting"} }, "tombstone": func(r *model.MemoryRecordV2) { r.Lifecycle = model.MemoryTombstoned }, "superseded": func(r *model.MemoryRecordV2) { r.SupersededBy = []string{"new"} }, "body": func(r *model.MemoryRecordV2) { r.Body = "Different approved scope" }} {
		t.Run(name, func(t *testing.T) {
			s, c, a, now := acceptedContextStore(t)
			admission := contextAdmission(t, s, c, a, now)
			resolved := make(chan struct{})
			mutated := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				close(resolved)
				<-mutated
				_, err := s.CommitProjectContextAdmission(t.Context(), admission)
				done <- err
			}()
			<-resolved
			// Explicit barrier orders canonical mutation after snapshot and before
			// transaction admission, without scheduler timing or synthetic authority.
			_, err := s.UpdateMemory(t.Context(), c.Project.StorageID, c.Required[0].ID, c.Required[0].Revision, func(r *model.MemoryRecordV2) error { mutate(r); return nil })
			if err != nil {
				t.Fatal(err)
			}
			close(mutated)
			if err = <-done; err == nil {
				t.Fatal("stale admission committed")
			}
			var n int
			if err = s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM extreme_project_context_receipts").Scan(&n); err != nil || n != 0 {
				t.Fatal("stale coverage was retained", n, err)
			}
		})
	}
}
func TestProjectContextInventoryIsExhaustiveAndOrdinaryCandidatesDoNotInvalidate(t *testing.T) {
	s, c, a, now := acceptedContextStore(t)
	before, err := s.ReadProjectContextSnapshot(t.Context(), c.Project, a, projectcontext.Selection{TaskID: "implement"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		r := contextStoreRecord(fmt.Sprintf("MEM-worker-%d", i), now)
		r.Lifecycle = model.MemoryCandidate
		r.Authority = model.AuthorityAgent
		r.ContentDigest = r.CanonicalDigest()
		if err = s.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.ReadProjectContextSnapshot(t.Context(), c.Project, a, projectcontext.Selection{TaskID: "implement"}, now)
	if err != nil || before.StateDigest != after.StateDigest {
		t.Fatal("worker candidate capture invalidated approved context", err)
	}
	r := contextStoreRecord("MEM-new-accepted-policy", now)
	r.Kind = model.MemoryKindSemantic
	if err = s.WriteMemoryV2(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadProjectContextSnapshot(t.Context(), c.Project, a, projectcontext.Selection{TaskID: "implement"}, now); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatal("unclassified accepted semantic policy became optional", err)
	}
	inventory, _, err := s.ProjectContractInventory(t.Context(), c.Project, a)
	if err != nil || len(inventory) != 2 {
		t.Fatal("accepted-source discovery was ranked or kind-limited", len(inventory), err)
	}
	a.Actor = "other"
	r.ACLScope = "owner"
	if _, err = s.UpdateMemory(t.Context(), r.ProjectID, r.ID, r.Revision, func(current *model.MemoryRecordV2) error { current.ACLScope = "owner"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ProjectContractInventory(t.Context(), c.Project, a); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal("inventory exposed inaccessible memory", err)
	}
}
func TestProjectContext95MigrationPreserves18CandidateRowsAndFailureRollsBack(t *testing.T) {
	s := projectStore(t)
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	original := []model.MemoryRecordV2{}
	for i := 0; i < 18; i++ {
		r := contextStoreRecord(fmt.Sprintf("MEM-legacy-%02d", i), now)
		r.Lifecycle = model.MemoryCandidate
		r.Authority = model.AuthorityAgent
		r.ExtMeta = map[string]any{"legacy_field": "preserve"}
		r.ContentDigest = r.CanonicalDigest()
		if err := s.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
		saved, err := s.GetMemoryV2(t.Context(), r.ProjectID, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		original = append(original, saved)
	}
	for _, table := range []string{"extreme_project_context_receipts", "extreme_project_contract_required_refs", "extreme_project_contract_active", "extreme_project_contract_revisions"} {
		if _, err := s.db.ExecContext(t.Context(), "DROP TABLE "+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(t.Context(), fmt.Sprintf("DELETE FROM schema_migrations WHERE version=%d", schemaExtremeProjectContext)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), fmt.Sprintf(`CREATE TRIGGER migration_context_fault BEFORE INSERT ON schema_migrations WHEN NEW.version=%d BEGIN SELECT RAISE(ABORT,'migration fault'); END`, schemaExtremeProjectContext)); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(t.Context()); err == nil {
		t.Fatal("failed private migration committed")
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE name='extreme_project_contract_revisions'").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial schema survived rollback", count, err)
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER migration_context_fault"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, before := range original {
		after, err := s.GetMemoryV2(t.Context(), before.ProjectID, before.ID)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("legacy candidate changed in migration", before.ID, err)
		}
	}
	var integrity string
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	rows, err := s.db.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("private migration introduced FK failure")
	}
}
func TestProjectContextTransactionSingleConnectionDoesNotReenterStore(t *testing.T) {
	s, c, a, now := acceptedContextStore(t)
	admission := contextAdmission(t, s, c, a, now)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := s.CommitProjectContextAdmission(ctx, admission); err != nil {
		t.Fatal("transaction reentered single-connection store", err)
	}
}

func TestProjectContextOnlyApplicableFactsAreAuthorizedReadAndInvalidate(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	alpha := contextStoreRecord("MEM-alpha", now)
	beta := contextStoreRecord("MEM-beta", now)
	beta.Scope = "task"
	beta.ScopeID = "task-beta"
	beta.ACLScope = "owner"
	beta.ContentDigest = beta.CanonicalDigest()
	private := contextStoreRecord("MEM-private-optional", now)
	private.Scope = "operator_private"
	private.ScopeID = "owner"
	private.ACLScope = "owner"
	private.ContentDigest = private.CanonicalDigest()
	for _, r := range []model.MemoryRecordV2{alpha, beta, private} {
		if err := s.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	c, owner, now := contextStoreDraft(t, s, []model.MemoryRecordV2{alpha, beta, private})
	owner.ScopeIDs = append(owner.ScopeIDs, "task-beta", "owner")
	c.Required[0].Applies = projectcontext.Applicability{TaskIDs: []string{"task-alpha"}}
	c.Required[1].Applies = projectcontext.Applicability{TaskIDs: []string{"task-beta"}}
	c.Required[2].Class = projectcontext.ContextOnly
	req := admitContextDecision(t, s, c, true, "scoped")
	accepted, err := s.CommitMCPProjectContractDecision(t.Context(), req, true, owner, now, contextAudit(req, now))
	if err != nil {
		t.Fatal(err)
	}
	worker := ProjectContextAudience{Actor: "worker-alpha", ScopeIDs: []string{"PROJECT-local"}, Worker: true}
	selection := projectcontext.Selection{TaskID: "task-alpha"}
	before, err := s.ReadProjectContextSnapshot(t.Context(), accepted.Project, worker, selection, now)
	if err != nil || len(before.Records) != 1 || before.Records[0].ID != alpha.ID {
		t.Fatal("irrelevant/private fact blocked or leaked to alpha", before, err)
	}
	for _, r := range []model.MemoryRecordV2{beta, private} {
		if _, err = s.UpdateMemory(t.Context(), r.ProjectID, r.ID, r.Revision, func(current *model.MemoryRecordV2) error {
			current.Body = "Unrelated beta observation changed"
			current.ACLScope = "another-owner"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.ReadProjectContextSnapshot(t.Context(), accepted.Project, worker, selection, now)
	if err != nil || before.StateDigest != after.StateDigest {
		t.Fatal("unrelated beta/private mutation invalidated alpha", err)
	}
	if _, err = s.UpdateMemory(t.Context(), alpha.ProjectID, alpha.ID, alpha.Revision, func(current *model.MemoryRecordV2) error { current.ACLScope = "owner"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadProjectContextSnapshot(t.Context(), accepted.Project, worker, selection, now); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal("applicable alpha ACL revocation was ignored", err)
	}
}

func TestProjectContext95PrivateSchemaIsNotAcceptance(t *testing.T) {
	s := projectStore(t)
	if canonicalMigrations[schemaExtremeProjectContext] == nil || LatestSchemaVersion < schemaExtremeProjectContext {
		t.Fatalf("private context schema missing: latest %d, want migration %d", LatestSchemaVersion, schemaExtremeProjectContext)
	}
	for _, table := range []string{"extreme_project_contract_revisions", "extreme_project_contract_active", "extreme_project_contract_required_refs", "extreme_project_context_receipts"} {
		var n int
		if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("missing private table %s: %d %v", table, n, err)
		}
		if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("migration manufactured project authority in %s: %d %v", table, n, err)
		}
	}
}

func TestProjectContextAcceptedDiscoveryUsesPrivatePartialIndexAcross100KCandidates(t *testing.T) {
	s, c, a, now := acceptedContextStore(t)
	// Ordinary candidates are outside the accepted-source registry even with
	// deliberately undecodable metadata. No worker may scan their bodies.
	if _, err := s.db.ExecContext(t.Context(), `WITH RECURSIVE candidates(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM candidates WHERE n<100000)
INSERT INTO memory_records_v2(memory_id,project_id,kind,lifecycle,authority,body,scope,scope_id,source_json,observed_at,ingested_at,valid_from,created_at,updated_at)
SELECT printf('MEM-optional-%06d',n),'PROJECT-local','fact','candidate','agent','optional body','project','PROJECT-local','not-json','2026-10-12T00:00:00Z','2026-10-12T00:00:00Z','2026-10-12T00:00:00Z','2026-10-12T00:00:00Z','2026-10-12T00:00:00Z' FROM candidates`); err != nil {
		t.Fatal(err)
	}
	for _, columns := range []string{"memory_id", "memory_id,content_digest,scope,scope_id"} {
		rows, err := s.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN SELECT "+columns+" FROM memory_records_v2 WHERE project_id=? AND lifecycle IN ('durable','verified') AND authority IN ('operator','policy') ORDER BY memory_id LIMIT ?", c.Project.StorageID, maxProjectContextRefs+1)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		plan := strings.Join(details, " / ")
		t.Log("accepted-source discovery:", plan)
		if !strings.Contains(plan, "extreme_project_accepted_memory_inventory") || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatal("accepted-source discovery scans/sorts irrelevant candidate corpus", plan)
		}
	}
	snapshot, err := s.ReadProjectContextSnapshot(t.Context(), c.Project, a, projectcontext.Selection{TaskID: "implement"}, now)
	if err != nil || len(snapshot.Records) != 1 || snapshot.Records[0].ID != c.Required[0].ID {
		t.Fatal("100K unrelated candidate rows entered required context", snapshot, err)
	}
}

func repacketContextAdmission(t *testing.T, s *Store, a *ProjectContextAdmission) {
	t.Helper()
	snapshot, err := s.ReadProjectContextSnapshot(t.Context(), a.Binding.Project, a.Audience, a.Selection, a.Now)
	if err != nil {
		t.Fatal(err)
	}
	goal, err := s.GetGoalContract(t.Context(), a.Binding.GoalID, a.Binding.GoalRevision)
	if err != nil {
		t.Fatal(err)
	}
	a.ExpectedStateDigest = snapshot.StateDigest
	a.Binding.RequiredStateDigest = snapshot.StateDigest
	packet, err := projectcontext.RenderWithPrefix(projectcontext.Manifest{Contract: snapshot.Contract, Goal: projectcontext.GoalFromCanonical(goal), Binding: a.Binding, Selection: a.Selection, Records: snapshot.Records}, a.Prefix, a.ByteCap)
	if err != nil {
		t.Fatal(err)
	}
	a.ExpectedPacketDigest = packet.Digest
}
func TestProjectContextAdmissionRequiresApprovedCurrentPlanAndGoal(t *testing.T) {
	s, c, a, now := acceptedContextStore(t)
	admission := contextAdmission(t, s, c, a, now)
	p := storedPlan()
	if err := s.SavePlan(t.Context(), p, 0); err != nil {
		t.Fatal(err)
	}
	admission.Stage = "dispatch"
	admission.Binding.PlanID = p.ID
	admission.Binding.PlanVersion = p.Version
	admission.Binding.ApprovalScopeDigest = "unreviewed"
	repacketContextAdmission(t, s, &admission)
	if _, err := s.CommitProjectContextAdmission(t.Context(), admission); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatal("ready plan dispatch admitted", err)
	}
	approved, err := p.Approve(now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SavePlan(t.Context(), approved, p.Version); err != nil {
		t.Fatal(err)
	}
	approved, err = s.GetActivePlan(t.Context(), planTestProject)
	if err != nil {
		t.Fatal(err)
	}
	admission.Binding.PlanVersion = approved.Version
	admission.Binding.ApprovalScopeDigest = approved.ApprovalScopeDigest
	repacketContextAdmission(t, s, &admission)
	receipt, err := s.CommitProjectContextAdmission(t.Context(), admission)
	if err != nil {
		t.Fatal("approved exact plan refused", err)
	}
	continuation := admission
	continuation.ID = "coverage-resume"
	continuation.Stage = "native_resume"
	continuation.ParentReceiptID = receipt.ID
	if _, err = s.CommitProjectContextAdmission(t.Context(), continuation); err != nil {
		t.Fatal("exact continuation coverage refused", err)
	}
	continuation.ID = "coverage-foreign-parent"
	continuation.Binding.TaskID = "other-task"
	repacketContextAdmission(t, s, &continuation)
	if _, err = s.CommitProjectContextAdmission(t.Context(), continuation); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatal("foreign task reused parent receipt", err)
	}
	goal, err := s.GetGoalContract(t.Context(), admission.Binding.GoalID, 1)
	if err != nil {
		t.Fatal(err)
	}
	goal.DesiredOutcome = "Current goal changed after approval"
	if err = s.SaveGoalContract(t.Context(), goal, 1); err != nil {
		t.Fatal(err)
	}
	admission.ID = "coverage-old-goal"
	if _, err = s.CommitProjectContextAdmission(t.Context(), admission); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatal("old goal dispatch admitted", err)
	}
}
func TestProjectContextRequiredCurrentEvidenceChangesBeforeAdmissionRefuse(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	node := testEvidenceNode("EVIDENCE-context", "claim", "verified-source")
	if _, err := s.PutNode(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	r := contextStoreRecord("MEM-evidence", now)
	r.EvidenceIDs = []string{string(node.ID)}
	r.LastVerifiedAt = &now
	r.ContentDigest = r.CanonicalDigest()
	if err := s.WriteMemoryV2(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	c, a, now := contextStoreDraft(t, s, []model.MemoryRecordV2{r})
	c.Required[0].EvidenceRequired = true
	c.Required[0].MaxVerificationAgeSeconds = 30
	req := admitContextDecision(t, s, c, true, "evidence")
	accepted, err := s.CommitMCPProjectContractDecision(t.Context(), req, true, a, now, contextAudit(req, now))
	if err != nil {
		t.Fatal(err)
	}
	admission := contextAdmission(t, s, accepted, a, now)
	if _, err = s.db.ExecContext(t.Context(), "UPDATE evidence_nodes SET state=? WHERE node_id=?", string(evidence.StateLinked), node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitProjectContextAdmission(t.Context(), admission); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatal("changed still-valid evidence reused old admission packet", err)
	}
	current, err := s.ReadProjectContextSnapshot(t.Context(), c.Project, a, projectcontext.Selection{TaskID: "implement"}, now)
	if err != nil || current.StateDigest == admission.ExpectedStateDigest {
		t.Fatal("current evidence absent from full state", err)
	}
	if _, err = s.db.ExecContext(t.Context(), "UPDATE evidence_nodes SET state=? WHERE node_id=?", string(evidence.StateArchived), node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadProjectContextSnapshot(t.Context(), c.Project, a, projectcontext.Selection{TaskID: "implement"}, now); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatal("archived evidence remained qualified", err)
	}
}

func TestProjectContextKnownContextOnlySemanticRevisionNeedsReclassification(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	r := contextStoreRecord("MEM-context-only", now)
	r.Kind = model.MemoryKindSemantic
	r.ContentDigest = r.CanonicalDigest()
	if err := s.WriteMemoryV2(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	c, a, now := contextStoreDraft(t, s, []model.MemoryRecordV2{r})
	c.Required[0].Class = projectcontext.ContextOnly
	req := admitContextDecision(t, s, c, true, "context-only")
	accepted, err := s.CommitMCPProjectContractDecision(t.Context(), req, true, a, now, contextAudit(req, now))
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadProjectContextSnapshot(t.Context(), accepted.Project, a, projectcontext.Selection{TaskID: "task-alpha"}, now)
	if err != nil || len(before.Records) != 0 {
		t.Fatal("context-only leaked as required", err)
	}
	metadata, err := s.UpdateMemory(t.Context(), r.ProjectID, r.ID, r.Revision, func(current *model.MemoryRecordV2) error {
		verified := now.Add(time.Second)
		current.LastVerifiedAt = &verified
		current.ACLScope = "another-reader"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.ReadProjectContextSnapshot(t.Context(), accepted.Project, a, projectcontext.Selection{TaskID: "task-alpha"}, now)
	if err != nil || after.StateDigest != before.StateDigest {
		t.Fatal("context-only ACL/verification metadata became a new policy", err)
	}
	if _, err = s.UpdateMemory(t.Context(), r.ProjectID, r.ID, metadata.Revision, func(current *model.MemoryRecordV2) error {
		current.Body = "Newly accepted mandatory project policy"
		current.Kind = model.MemoryKindDecision
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadProjectContextSnapshot(t.Context(), accepted.Project, a, projectcontext.Selection{TaskID: "task-alpha"}, now); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatal("existing ContextOnly ID silently hid new semantic acceptance", err)
	}
}
func TestProjectContextBetaOnlyAcceptedRevisionTranslatesAlphaButArchitectureDoesNot(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	alpha := contextStoreRecord("MEM-alpha", now)
	beta := contextStoreRecord("MEM-beta", now)
	for _, r := range []model.MemoryRecordV2{alpha, beta} {
		if err := s.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	c, a, now := contextStoreDraft(t, s, []model.MemoryRecordV2{alpha, beta})
	c.Required[0].Applies = projectcontext.Applicability{TaskIDs: []string{"implement"}}
	c.Required[1].Applies = projectcontext.Applicability{TaskIDs: []string{"task-beta"}}
	req := admitContextDecision(t, s, c, true, "translation-first")
	accepted, err := s.CommitMCPProjectContractDecision(t.Context(), req, true, a, now, contextAudit(req, now))
	if err != nil {
		t.Fatal(err)
	}
	admission := contextAdmission(t, s, accepted, a, now)
	oldRef := admission.Binding.Contract
	changed, err := s.UpdateMemory(t.Context(), beta.ProjectID, beta.ID, beta.Revision, func(r *model.MemoryRecordV2) error { r.Body = "New beta-only required policy"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.ReadProjectContextSnapshot(t.Context(), accepted.Project, a, admission.Selection, now)
	if err != nil || unchanged.StateDigest != admission.ExpectedStateDigest {
		t.Fatal("unrelated beta source mutation invalidated alpha", err)
	}
	next := accepted
	next.Revision = 2
	next.RevisionReason = "Review beta-only policy revision"
	next.SupersedesDigest = oldRef.Digest
	next.Required = append([]projectcontext.MemoryRef(nil), accepted.Required...)
	next.Required[1].Revision = changed.Revision
	next.Required[1].ContentDigest = changed.ContentDigest
	next.Required[1].StateFingerprint = projectcontext.MemoryFingerprint(changed)
	req = admitContextDecision(t, s, next, true, "translation-beta")
	current, err := s.CommitMCPProjectContractDecision(t.Context(), req, true, a, now, contextAudit(req, now))
	if err != nil {
		t.Fatal(err)
	}
	translated, err := s.ReadProjectContextSnapshotBound(t.Context(), current.Project, a, admission.Selection, now, oldRef)
	if err != nil || translated.TranslationDigest == "" || translated.CurrentContract.Revision != 2 || translated.Contract.Revision != 1 || translated.StateDigest != admission.ExpectedStateDigest {
		t.Fatal("safe alpha compatibility translation missing", translated, err)
	}
	receipt, err := s.CommitProjectContextAdmission(t.Context(), admission)
	if err != nil || receipt.CurrentContract.Revision != 2 || receipt.Binding.Contract != oldRef || receipt.TranslationDigest == "" || receipt.PacketDigest != admission.ExpectedPacketDigest {
		t.Fatal("beta-only acceptance discarded exact reviewed alpha packet", receipt, err)
	}
	next = current
	next.Revision = 3
	next.RevisionReason = "Architecture purpose changed"
	next.SupersedesDigest = translated.CurrentContract.Digest
	next.Purpose = "Changed project-wide purpose"
	req = admitContextDecision(t, s, next, true, "translation-architecture")
	if _, err = s.CommitMCPProjectContractDecision(t.Context(), req, true, a, now, contextAudit(req, now)); err != nil {
		t.Fatal(err)
	}
	admission.ID = "old-architecture"
	if _, err = s.CommitProjectContextAdmission(t.Context(), admission); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatal("changed architecture reused old reviewed packet", err)
	}
}

func TestProjectContextInspectionAndAuthorizedMetadataPagesDoNotQualifyOrLeak(t *testing.T) {
	s, c, a, now := acceptedContextStore(t)
	inspected, err := s.ReadAcceptedProjectContract(t.Context(), c.Project)
	if err != nil || inspected.Version != 1 || inspected.Digest == "" {
		t.Fatal(inspected, err)
	}
	private := contextStoreRecord("MEM-private-page", now)
	private.Scope = "operator_private"
	private.ScopeID = "owner"
	private.ACLScope = "owner"
	private.ContentDigest = private.CanonicalDigest()
	if err = s.WriteMemoryV2(t.Context(), private); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadProjectContractInventoryPage(t.Context(), c.Project, ProjectContextAudience{Actor: "worker-alpha", ScopeIDs: []string{"PROJECT-local"}, Worker: true}, "", 1); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal("worker obtained hidden-ID completeness digest", err)
	}
	page, err := s.ReadProjectContractInventoryPage(t.Context(), c.Project, ProjectContextAudience{Actor: "limited-owner", ScopeIDs: []string{"PROJECT-local"}}, "", 1)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != c.Required[0].ID || page.NextAfterID != "" || page.CompletenessDigest == "" {
		t.Fatal("private existence leaked or public metadata missing", page, err)
	}
	raw, _ := json.Marshal(page)
	if string(raw) == "" || bytes.Contains(raw, []byte(private.ID)) || bytes.Contains(raw, []byte(private.Body)) || bytes.Contains(raw, []byte("acl_scope")) {
		t.Fatal("metadata leaked private/body/ACL data", string(raw))
	}
	owner := a
	owner.ScopeIDs = append(owner.ScopeIDs, "owner")
	first, err := s.ReadProjectContractInventoryPage(t.Context(), c.Project, owner, "", 1)
	if err != nil || first.NextAfterID == "" {
		t.Fatal(first, err)
	}
	second, err := s.ReadProjectContractInventoryPage(t.Context(), c.Project, owner, first.NextAfterID, 1)
	if err != nil || len(second.Entries) != 1 || first.Entries[0].ID == second.Entries[0].ID || first.CompletenessDigest != second.CompletenessDigest {
		t.Fatal("stable exact pagination failed", second, err)
	}
	if _, err = s.ReadProjectContractInventoryPage(t.Context(), c.Project, owner, "", 9); !errors.Is(err, projectcontext.ErrInvalid) {
		t.Fatal("unbounded page accepted", err)
	}
	inspected, err = s.ReadAcceptedProjectContract(t.Context(), c.Project)
	if err != nil || inspected.Version != 1 {
		t.Fatal("stale refs prevented read-only inspection", err)
	}
	if _, err = s.ReadProjectContextSnapshot(t.Context(), c.Project, a, projectcontext.Selection{TaskID: "implement"}, now); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatal("inspection page forged qualified coverage", err)
	}
}

func TestProjectContextAdmissionDurableReceiptFaultRollbackAndUnavailableRefusal(t *testing.T) {
	for _, fault := range []string{`CREATE TRIGGER context_receipt_fault BEFORE INSERT ON extreme_project_context_receipts BEGIN SELECT RAISE(ABORT,'coverage fault'); END`, `CREATE TRIGGER context_receipt_fault BEFORE INSERT ON audit_events WHEN NEW.event_type='PROJECT_CONTEXT_QUALIFIED' BEGIN SELECT RAISE(ABORT,'audit fault'); END`} {
		t.Run(fault[56:65], func(t *testing.T) {
			s, c, a, now := acceptedContextStore(t)
			admission := contextAdmission(t, s, c, a, now)
			if _, err := s.db.ExecContext(t.Context(), fault); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CommitProjectContextAdmission(t.Context(), admission); err == nil {
				t.Fatal("failed coverage effect reported success")
			}
			var n int
			if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM extreme_project_context_receipts").Scan(&n); err != nil || n != 0 {
				t.Fatal("partial coverage survived fault", n, err)
			}
			if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER context_receipt_fault"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CommitProjectContextAdmission(t.Context(), admission); err != nil {
				t.Fatal("unchanged supported retry failed", err)
			}
		})
	}
	s, c, a, now := acceptedContextStore(t)
	admission := contextAdmission(t, s, c, a, now)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.CommitProjectContextAdmission(ctx, admission); !errors.Is(err, context.Canceled) {
		t.Fatal("unavailable transaction admitted", err)
	}
	if _, err := s.ReadProjectContextSnapshot(ctx, c.Project, a, admission.Selection, now); !errors.Is(err, context.Canceled) {
		t.Fatal("unavailable source returned current", err)
	}
	admission.Stage = "unrecognized"
	if _, err := s.CommitProjectContextAdmission(t.Context(), admission); !errors.Is(err, projectcontext.ErrInvalid) {
		t.Fatal("unknown admission stage accepted", err)
	}
	admission.Stage = "planning"
	admission.ParentReceiptID = "unknown-parent"
	if _, err := s.CommitProjectContextAdmission(t.Context(), admission); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatal("unknown parent receipt accepted", err)
	}
}
func TestProjectContextExistingJournalLabelsCannotForgeDecision(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	r := contextStoreRecord("MEM-policy", now)
	if err := s.WriteMemoryV2(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	c, a, now := contextStoreDraft(t, s, []model.MemoryRecordV2{r})
	req := admitContextDecision(t, s, c, true, "journal-forgery")
	for _, change := range []func(*permission.Request){func(q *permission.Request) { q.SourceLaunch = "foreign-launch" }, func(q *permission.Request) { q.ApprovalBinding = "caller-accepted" }, func(q *permission.Request) { q.Object = "foreign-contract" }, func(q *permission.Request) { q.Reason = "altered reviewed reason" }} {
		q := req
		change(&q)
		if _, err := s.CommitMCPProjectContractDecision(t.Context(), q, true, a, now, contextAudit(q, now)); err == nil {
			t.Fatal("caller label widened original source")
		}
	}
	if _, err := s.CommitMCPProjectContractDecision(t.Context(), req, false, a, now, contextAudit(req, now)); err == nil {
		t.Fatal("decision allow was reinterpreted as deny")
	}
	raw := []byte(`{"action":"decide","id":"Q","allow":false,"allow":true,"reason":"dup"}`)
	var dst any
	if err := decodeProjectDecision(raw, &dst); err == nil {
		t.Fatal("duplicate answer fields accepted")
	}
	if err := decodeProjectDecision([]byte(`{"action":"decide"} trailing`), &dst); err == nil {
		t.Fatal("trailing malformed JSON accepted")
	}
}
