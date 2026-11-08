//go:build !governed

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/verification"
)

// ReadProjectContextReceipt is a trusted exact inspection read. It conveys no
// current qualification and must not be exposed as an unscoped worker tool.
func (s *Store) ReadProjectContextReceipt(ctx context.Context, id string) (projectcontext.Receipt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return projectcontext.Receipt{}, err
	}
	defer tx.Rollback()
	return readProjectContextReceiptTx(ctx, tx, id)
}

func readProjectContextReceiptTx(ctx context.Context, tx *sql.Tx, id string) (projectcontext.Receipt, error) {
	var raw, bound, project, contract, stage, outcome, packet string
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT receipt_json,binding_json,project_id,contract_id,contract_revision,stage,outcome,packet_digest FROM extreme_project_context_receipts WHERE receipt_id=?`, id).Scan(&raw, &bound, &project, &contract, &revision, &stage, &outcome, &packet)
	if errors.Is(err, sql.ErrNoRows) {
		return projectcontext.Receipt{}, projectcontext.ErrMissing
	}
	if err != nil {
		return projectcontext.Receipt{}, err
	}
	var r projectcontext.Receipt
	var binding projectcontext.Binding
	if json.Unmarshal([]byte(raw), &r) != nil || json.Unmarshal([]byte(bound), &binding) != nil || r.ID != id || r.Binding != binding || r.Binding.Project.CanonicalID != project || r.CurrentContract.ID != contract || r.CurrentContract.Revision != revision || r.Stage != stage || r.Outcome != outcome || r.PacketDigest != packet {
		return projectcontext.Receipt{}, projectcontext.Changed("project_context_completion_extreme.go:46")
	}
	return r, nil
}

// CommitProjectContextCompletion is an internal protected effect. The caller
// holds the current run/repository/owner fence and supplies its parent-bound
// admissions. Every approved task is requalified in the SAME short SQLite
// transaction as the canonical attestation and immutable binding audit.
// An authenticated independent architecture producer must have already
// created its exact canonical COMPATIBLE receipt; caller reports never suffice.
func (s *Store) CommitProjectContextCompletion(ctx context.Context, admissions []ProjectContextAdmission, envelope verification.BundleEnvelope, attestation verification.CompletionAttestation, architectureReceiptID string) ([]projectcontext.Receipt, error) {
	if len(admissions) == 0 || len(admissions) > maxProjectContextRefs || architectureReceiptID == "" {
		return nil, projectcontext.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	first := admissions[0]
	// Reserve writer BEFORE reading current contract, plan, receipt or verifier.
	if _, err = tx.ExecContext(ctx, `UPDATE extreme_project_contract_active SET cas_version=cas_version WHERE project_id=?`, first.Binding.Project.CanonicalID); err != nil {
		return nil, err
	}
	contract, _, currentDigest, err := readProjectContract(ctx, tx, first.Binding.Project)
	if err != nil {
		return nil, err
	}
	currentRef := projectcontext.ContractRef{ID: contract.ID, Revision: contract.Revision, Digest: currentDigest}
	compatible, err := readProjectContextReceiptTx(ctx, tx, architectureReceiptID)
	if err != nil {
		return nil, err
	}
	if compatible.Stage != "architecture_compatibility" || compatible.Outcome != "COMPATIBLE" || compatible.ReasonCode == "" || !projectCompletionEvidenceDigest(compatible.CompatibilityEvidenceDigest) || compatible.CurrentContract != currentRef || compatible.Binding.Contract != currentRef || compatible.CheckedAt.IsZero() || first.Now.Before(compatible.CheckedAt) || (compatible.ValidUntil != nil && !first.Now.Before(*compatible.ValidUntil)) || !projectCompletionParentBinding(compatible.Binding, first.Binding) {
		return nil, projectcontext.Changed("project_context_completion_extreme.go:81")
	}
	if err = validateProjectContextPlan(ctx, tx, first.Binding); err != nil {
		return nil, err
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT plan_json FROM execution_plans WHERE plan_id=? AND version=?`, first.Binding.PlanID, first.Binding.PlanVersion).Scan(&raw); err != nil {
		return nil, err
	}
	p, err := decodePlan(raw)
	if err != nil || len(p.Tasks) != len(admissions) {
		return nil, projectcontext.Changed("project_context_completion_extreme.go:92")
	}
	byTask := map[string]ProjectContextAdmission{}
	seenReceipts := map[string]bool{}
	for _, a := range admissions {
		if a.Stage != "completion" || !projectCompletionParentBinding(a.Binding, first.Binding) || a.Now != first.Now || a.Audience.Actor != first.Audience.Actor || a.Binding.Contract != first.Binding.Contract || seenReceipts[a.ID] {
			return nil, projectcontext.Changed("project_context_completion_extreme.go:98")
		}
		if _, duplicate := byTask[a.Binding.TaskID]; duplicate {
			return nil, projectcontext.Changed("project_context_completion_extreme.go:101")
		}
		byTask[a.Binding.TaskID] = a
		seenReceipts[a.ID] = true
	}
	for _, task := range p.Tasks {
		a, exists := byTask[task.ID]
		if !exists || !reflect.DeepEqual(projectCompletionSelection(p, task, contract), projectCompletionNormalizeSelection(a.Selection)) {
			return nil, projectcontext.Changed("project_context_completion_extreme.go:109")
		}
	}
	var sessionRaw []byte
	if err = tx.QueryRowContext(ctx, `SELECT session_json FROM verification_sessions WHERE verification_id=? AND version=?`, attestation.VerificationID, attestation.VerificationVersion).Scan(&sessionRaw); err != nil {
		return nil, verification.ErrConflict
	}
	var session verification.Session
	if json.Unmarshal(sessionRaw, &session) != nil || verification.ValidateSession(session) != nil || !projectCompletionVerificationBinding(first.Binding, session.Binding) || session.State != verification.VerifiedComplete || verification.Evaluate(session, session.Binding, first.Now) != verification.VerifiedComplete || attestation.Decision != verification.VerifiedComplete || envelope.Bundle.VerificationID != session.ID {
		return nil, verification.ErrBindingMismatch
	}
	if err = envelope.Bundle.Verify(envelope.Payloads, session.Binding); err != nil {
		return nil, err
	}
	if err = attestation.Verify(session.Binding); err != nil {
		return nil, err
	}
	expected, err := verification.NewCompletionAttestation(attestation.ID, session, envelope.Bundle.ManifestDigest, attestation.Provenance, first.Now)
	if err != nil || !reflect.DeepEqual(expected, attestation) {
		return nil, verification.ErrBindingMismatch
	}
	if err = validateProjectArchitectureCompletionTx(ctx, tx, compatible, session, envelope, first.Now); err != nil {
		return nil, err
	}
	receipts := make([]projectcontext.Receipt, 0, len(admissions))
	for _, a := range admissions {
		receipt, err := s.commitProjectContextAdmissionTx(ctx, tx, a)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	body, err := json.Marshal(attestation)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO completion_attestations(attestation_id,verification_id,verification_version,decision,digest,attestation_json,issued_at) VALUES(?,?,?,?,?,?,?)`, attestation.ID, attestation.VerificationID, attestation.VerificationVersion, attestation.Decision, attestation.Digest, body, attestation.IssuedAt.UTC().Format(timeLayout)); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(receipts))
	for _, receipt := range receipts {
		ids = append(ids, receipt.ID)
	}
	sort.Strings(ids)
	if err = s.AppendEvent(ctx, tx, model.Event{ID: "context-completion-" + attestation.ID, ProjectID: first.Binding.Project.StorageID, Type: "PROJECT_CONTEXT_COMPLETION_BOUND", Timestamp: first.Now, Data: map[string]any{"attestation_id": attestation.ID, "attestation_digest": attestation.Digest, "architecture_receipt_id": compatible.ID, "compatibility_evidence_digest": compatible.CompatibilityEvidenceDigest, "task_receipt_ids": ids, "checked_tree": session.Binding.TreeDigest, "current_contract_digest": currentRef.Digest}}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return receipts, nil
}

func projectCompletionEvidenceDigest(s string) bool {
	if len(s) != 71 || s[:7] != "sha256:" {
		return false
	}
	b, err := hex.DecodeString(s[7:])
	return err == nil && len(b) == 32
}

func projectCompletionParentBinding(a, b projectcontext.Binding) bool {
	return a.Project == b.Project && a.GoalID == b.GoalID && a.GoalRevision == b.GoalRevision && a.PlanID == b.PlanID && a.PlanVersion == b.PlanVersion && a.ApprovalScopeDigest == b.ApprovalScopeDigest && a.RunID == b.RunID && a.RunID != "" && a.RunVersion == b.RunVersion && a.RunVersion > 0 && a.Tree == b.Tree && a.Tree != "" && a.Principal == b.Principal && a.Session == b.Session
}

func projectCompletionVerificationBinding(a projectcontext.Binding, b verification.Binding) bool {
	return a.Project.CanonicalID == b.ProjectID && a.GoalID == b.GoalID && a.GoalRevision == b.GoalRevision && a.PlanID == b.PlanID && a.PlanVersion == b.PlanVersion && a.RunID == b.RunID && a.RunVersion == b.RunVersion && a.Tree == b.TreeDigest
}

// This is the existing Process05 identity encoding, never a caller label.
func projectCompletionCanonicalTaskID(planID string, version int64, taskID string) string {
	sum := sha256.Sum256([]byte(planID + "\x00" + fmt.Sprint(version) + "\x00" + taskID))
	return "TASK-P05-" + hex.EncodeToString(sum[:12])
}

func projectCompletionNormalizeSelection(s projectcontext.Selection) projectcontext.Selection {
	normalize := func(values []string) []string {
		seen := map[string]bool{}
		out := []string{}
		for _, value := range values {
			if !seen[value] {
				out = append(out, value)
				seen[value] = true
			}
		}
		sort.Strings(out)
		return out
	}
	s.Components = normalize(s.Components)
	s.Paths = normalize(s.Paths)
	return s
}

func projectCompletionSelection(p plan.ExecutionPlan, task plan.Task, c projectcontext.Contract) projectcontext.Selection {
	paths := append([]string(nil), task.Paths...)
	if policy, ok := p.Policy.PolicyFor(task.ID); ok {
		paths = append(paths, policy.AllowedScope...)
	}
	s := projectcontext.Selection{TaskID: task.ID, CanonicalTaskID: projectCompletionCanonicalTaskID(p.ID, p.Version, task.ID), Paths: sortedProjectScopes(paths), Components: []string{}}
	for _, component := range c.Components {
		if len(s.Paths) == 0 || len(component.Paths) == 0 || (projectcontext.Applicability{Paths: component.Paths}).Matches(s) {
			s.Components = append(s.Components, component.ID)
		}
	}
	return projectCompletionNormalizeSelection(s)
}
