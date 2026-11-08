//go:build !governed

package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/verification"
)

// These records are observation DATA, never proof of a live invocation or an
// authority grant. Only the authenticated runtime producer may call the writer
// after checking its unexported successful-turn token and owned source/run fence.
type ProjectArchitectureInvocation struct {
	ID, Provider, ObservedModel, SessionID, PriorSessionID string
	BinaryDigest, InputDigest, OutputDigest, PayloadDigest string
	SourceHead, WorkspaceTree, ContextReceiptID            string
	StartedAt, FinishedAt                                  time.Time
}
type ProjectArchitectureAssessment struct {
	ObligationID, Status, Reason string
	EvidenceRefs                 []string
}
type ProjectArchitectureObservation struct {
	ID, VerificationID  string
	VerificationVersion int64
	InputDigest         string
	Invocation          ProjectArchitectureInvocation
	Assessments         []ProjectArchitectureAssessment
}
type ProjectArchitectureReviewInput struct {
	Binding                                         verification.Binding
	VerificationID                                  string
	VerificationVersion                             int64
	Contract                                        projectcontext.ContractRef
	PlanDigest, SessionDigest, BundleDigest, Digest string
	Obligations                                     []string
	EvidenceRefs                                    []string
	Packet                                          projectcontext.Packet
}

type projectArchitectureEvidenceRecord struct {
	ReceiptID                   string                              `json:"receipt_id"`
	Input                       ProjectArchitectureReviewInput      `json:"input"`
	CompatibilityEvidenceDigest string                              `json:"compatibility_evidence_digest"`
	Observation                 ProjectArchitectureObservation      `json:"observation"`
	Audience                    ProjectContextAudience              `json:"audience"`
	Authority                   *ProjectContextAuthorityObservation `json:"authority"`
}

func (s *Store) ReadProjectArchitectureReview(ctx context.Context, a ProjectContextAdmission, verificationID string, version int64, envelope verification.BundleEnvelope) (ProjectArchitectureReviewInput, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ProjectArchitectureReviewInput{}, err
	}
	defer tx.Rollback()
	input, err := readProjectArchitectureReviewTx(ctx, tx, a, verificationID, version, envelope)
	if err != nil {
		return ProjectArchitectureReviewInput{}, err
	}
	if err = tx.Commit(); err != nil {
		return ProjectArchitectureReviewInput{}, err
	}
	return input, nil
}

// This writer authenticates canonical SQL DATA, not a CLI. The application
// additionally spends its successful-turn token and holds its current owned
// owner/Engine/root/child/source fence. There is deliberately no MCP endpoint.
func (s *Store) CommitProjectArchitectureCompatibility(ctx context.Context, a ProjectContextAdmission, observation ProjectArchitectureObservation, envelope verification.BundleEnvelope) (projectcontext.Receipt, error) {
	zero := projectcontext.Receipt{}
	if a.ExpectedAuthority == nil || a.ExpectedAuthority.Request.Purpose != ProjectContextAuthorityCompletion || observation.ID == "" || observation.Invocation.ID == "" || observation.Invocation.ContextReceiptID == "" {
		return zero, projectcontext.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	// Reserve the writer BEFORE current contract/goal/plan/verifier reads.
	if _, err = tx.ExecContext(ctx, `UPDATE extreme_project_contract_active SET cas_version=cas_version WHERE project_id=?`, a.Binding.Project.CanonicalID); err != nil {
		return zero, err
	}
	input, err := readProjectArchitectureReviewTx(ctx, tx, a, observation.VerificationID, observation.VerificationVersion, envelope)
	if err != nil {
		return zero, err
	}
	if observation.InputDigest != input.Digest || a.ExpectedPacketDigest != input.Packet.Digest {
		return zero, projectcontext.Changed("project_context_architecture_extreme.go:100")
	}
	parent, err := readProjectContextReceiptTx(ctx, tx, observation.Invocation.ContextReceiptID)
	if err != nil {
		return zero, err
	}
	if parent.ID != a.ID || parent.Stage != "review" || a.Stage != "review" || parent.Outcome != "QUALIFIED" || parent.CurrentContract != input.Contract || parent.Binding != a.Binding || parent.PacketDigest != input.Packet.Digest || !reflect.DeepEqual(parent.Loaded, input.Packet.Loaded) || parent.RenderedBytes != input.Packet.Bytes || parent.RequiredCount != len(input.Packet.Loaded) || parent.IncludedCount != parent.RequiredCount || parent.ParentReceiptID != a.ParentReceiptID || a.Now.Before(parent.CheckedAt) || parent.ValidUntil != nil && !a.Now.Before(*parent.ValidUntil) {
		return zero, projectcontext.Changed("project_context_architecture_extreme.go:107")
	}
	inv := observation.Invocation
	if inv.PriorSessionID != "" || !projectArchitectureObject(inv.SourceHead) || inv.WorkspaceTree != input.Binding.TreeDigest || !projectCompletionEvidenceDigest(inv.BinaryDigest) || !projectCompletionEvidenceDigest(inv.InputDigest) || !projectCompletionEvidenceDigest(inv.OutputDigest) || !projectCompletionEvidenceDigest(inv.PayloadDigest) || inv.StartedAt.IsZero() || inv.StartedAt.Before(parent.CheckedAt) || inv.FinishedAt.Before(inv.StartedAt) || a.Now.Before(inv.FinishedAt) {
		return zero, projectcontext.Changed("project_context_architecture_extreme.go:111")
	}
	if inv.Provider != "codex" && inv.Provider != "claude" && inv.Provider != "agy" && inv.Provider != "opencode" {
		return zero, projectcontext.ErrInvalid
	}
	for _, text := range []string{observation.ID, inv.ID, inv.ObservedModel, inv.SessionID} {
		if len(text) > 512 || !utf8.ValidString(text) || strings.ContainsAny(text, "\x00\r\n") {
			return zero, projectcontext.ErrInvalid
		}
	}
	if parent.ValidUntil != nil && !inv.FinishedAt.Before(*parent.ValidUntil) {
		return zero, projectcontext.Changed("project_context_architecture_extreme.go:122")
	}
	var reused int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE event_id=?`, "architecture-invocation-"+inv.ID).Scan(&reused); err != nil || reused != 0 {
		return zero, projectcontext.Changed("project_context_architecture_extreme.go:126")
	}
	if inv.SessionID != "" {
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE event_id=?`, projectArchitectureSessionKey(inv)).Scan(&reused); err != nil || reused != 0 {
			return zero, projectcontext.Changed("project_context_architecture_extreme.go:130")
		}
	}
	outcome, err := projectArchitectureOutcome(input, observation)
	if err != nil {
		return zero, err
	}
	body, err := json.Marshal(observation)
	if err != nil || len(body) > projectcontext.MaximumPacketBytes {
		return zero, projectcontext.ErrBudget
	}
	digest := projectcontext.Digest("marshal.project.architecture.observation.v1", struct {
		Input       string
		Observation ProjectArchitectureObservation
	}{input.Digest, observation})
	receipt := parent
	receipt.ID, receipt.Stage, receipt.ParentReceiptID = observation.ID, "architecture_compatibility", parent.ID
	receipt.Outcome, receipt.ReasonCode, receipt.CompatibilityEvidenceDigest = outcome, "CANONICAL_INDEPENDENT_ARCHITECTURE_OBSERVATION", digest
	receipt.CheckedAt = a.Now.UTC()
	bound, err := json.Marshal(receipt.Binding)
	if err != nil {
		return zero, err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return zero, err
	}
	var expiry any
	if receipt.ValidUntil != nil {
		expiry = receipt.ValidUntil.UTC().Format(time.RFC3339Nano)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO extreme_project_context_receipts(receipt_id,project_id,contract_id,contract_revision,stage,parent_receipt_id,manifest_digest,packet_digest,binding_json,receipt_json,outcome,required_count,included_count,rendered_bytes,created_at,valid_until) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, receipt.ID, receipt.Binding.Project.CanonicalID, receipt.CurrentContract.ID, receipt.CurrentContract.Revision, receipt.Stage, parent.ID, receipt.Binding.ManifestDigest, receipt.PacketDigest, string(bound), string(raw), receipt.Outcome, receipt.RequiredCount, receipt.IncludedCount, receipt.RenderedBytes, receipt.CheckedAt.Format(time.RFC3339Nano), expiry); err != nil {
		return zero, err
	}
	persistedInput := input
	persistedInput.Packet.Text = "" // Never persist a second copy of memory bodies/prompt.
	record := projectArchitectureEvidenceRecord{ReceiptID: receipt.ID, Input: persistedInput, CompatibilityEvidenceDigest: digest, Observation: observation, Audience: a.Audience, Authority: a.ExpectedAuthority}
	recordBody, err := json.Marshal(record)
	if err != nil || len(recordBody) > projectcontext.MaximumPacketBytes {
		return zero, projectcontext.ErrBudget
	}
	// Preserve int64 revisions exactly; converting through map[string]any JSON
	// would round versions greater than 2^53 through a float64.
	recordData := map[string]any{"receipt_id": record.ReceiptID, "input": record.Input, "compatibility_evidence_digest": record.CompatibilityEvidenceDigest, "observation": record.Observation, "audience": record.Audience, "authority": record.Authority}
	if err = s.AppendEvent(ctx, tx, model.Event{ID: "architecture-receipt-" + receipt.ID, ProjectID: a.Binding.Project.StorageID, Type: "PROJECT_ARCHITECTURE_COMPATIBILITY_RECORDED", Timestamp: a.Now, Data: recordData}); err != nil {
		return zero, err
	}
	if err = s.AppendEvent(ctx, tx, model.Event{ID: "architecture-invocation-" + inv.ID, ProjectID: a.Binding.Project.StorageID, Type: "PROJECT_ARCHITECTURE_INVOCATION_BOUND", Timestamp: a.Now, Data: map[string]any{"receipt_id": receipt.ID, "invocation_id": inv.ID}}); err != nil {
		return zero, err
	}
	if inv.SessionID != "" {
		if err = s.AppendEvent(ctx, tx, model.Event{ID: projectArchitectureSessionKey(inv), ProjectID: a.Binding.Project.StorageID, Type: "PROJECT_ARCHITECTURE_SESSION_BOUND", Timestamp: a.Now, Data: map[string]any{"receipt_id": receipt.ID, "invocation_id": inv.ID, "provider": inv.Provider, "session_id": inv.SessionID}}); err != nil {
			return zero, err
		}
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return receipt, nil
}

func readProjectArchitectureReviewTx(ctx context.Context, tx *sql.Tx, a ProjectContextAdmission, verificationID string, version int64, envelope verification.BundleEnvelope) (ProjectArchitectureReviewInput, error) {
	zero := ProjectArchitectureReviewInput{}
	b := a.Binding
	if a.Now.IsZero() || a.ByteCap < 1 || a.ByteCap > projectcontext.MaximumPacketBytes || b.FormatVersion != projectcontext.FormatVersion || b.RunID == "" || b.RunVersion < 1 || b.TaskID != "" || b.CanonicalTaskID != "" || b.Provider != "" || b.Model != "" || b.Attempt != 0 || b.Principal == "" || b.Principal != a.Audience.Actor || b.Session == "" || !projectArchitectureTree(b.Tree) || verificationID == "" || version < 1 {
		return zero, projectcontext.ErrInvalid
	}
	contract, _, contractDigest, err := readProjectContract(ctx, tx, b.Project)
	if err != nil {
		return zero, err
	}
	ref := projectcontext.ContractRef{ID: contract.ID, Revision: contract.Revision, Digest: contractDigest}
	if b.Contract != ref {
		return zero, projectcontext.Changed("project_context_architecture_extreme.go:203")
	}
	if err = validateProjectContextPlan(ctx, tx, b); err != nil {
		return zero, err
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT plan_json FROM execution_plans WHERE plan_id=? AND version=?`, b.PlanID, b.PlanVersion).Scan(&raw); err != nil {
		return zero, err
	}
	p, err := decodePlan(raw)
	if err != nil || p.RequiredProjectContract == nil || *p.RequiredProjectContract != ref {
		return zero, projectcontext.Changed("project_context_architecture_extreme.go:214")
	}
	var goalID string
	var goalRevision int64
	if err = tx.QueryRowContext(ctx, `SELECT active_goal_id,active_revision FROM goal_active WHERE session_id=?`, b.Session).Scan(&goalID, &goalRevision); err != nil || goalID != b.GoalID || goalRevision != b.GoalRevision {
		return zero, projectcontext.Changed("project_context_architecture_extreme.go:219")
	}
	goal, err := readGoalContract(ctx, tx, goalID, goalRevision)
	if err != nil || goal.SessionID != b.Session || goal.ProjectID != b.Project.CanonicalID && goal.ProjectID != b.Project.StorageID {
		return zero, projectcontext.Changed("project_context_architecture_extreme.go:223")
	}
	selection, err := projectcontext.PlanningSelection(contract)
	if err != nil || !reflect.DeepEqual(selection, a.Selection) {
		return zero, projectcontext.Changed("project_context_architecture_extreme.go:227")
	}
	snapshot, err := readProjectContextSnapshotBound(ctx, tx, b.Project, a.Audience, selection, a.Now, b.Contract)
	if err != nil {
		return zero, err
	}
	if snapshot.CurrentContract != ref || a.ExpectedStateDigest != snapshot.StateDigest || b.RequiredStateDigest != snapshot.StateDigest {
		return zero, projectcontext.Changed("project_context_architecture_extreme.go:234")
	}
	if expected := a.ExpectedAuthority; expected != nil {
		if expected.Request.Purpose != ProjectContextAuthorityCompletion || expected.CheckedAt.IsZero() || expected.CheckedAt.After(a.Now) || expected.ValidUntil != nil && !a.Now.Before(*expected.ValidUntil) || normalizeProjectAuthorityBinding(expected.Request.Binding) != normalizeProjectAuthorityBinding(b) {
			return zero, projectcontext.Changed("project_context_architecture_extreme.go:238")
		}
		current, err := readProjectContextAuthorityTx(ctx, tx, expected.Request, a.Now)
		if err != nil {
			return zero, err
		}
		if current.Digest != expected.Digest {
			return zero, projectcontext.Changed("project_context_architecture_extreme.go:245")
		}
	}
	packet, err := projectcontext.RenderWithPrefix(projectcontext.Manifest{Contract: contract, Goal: projectcontext.GoalFromCanonical(goal), Binding: b, Selection: selection, Records: snapshot.Records}, a.Prefix, a.ByteCap)
	if err != nil {
		return zero, err
	}
	var sessionRaw string
	if err = tx.QueryRowContext(ctx, `SELECT session_json FROM verification_sessions WHERE verification_id=? AND version=?`, verificationID, version).Scan(&sessionRaw); err != nil {
		return zero, verification.ErrConflict
	}
	var session verification.Session
	if json.Unmarshal([]byte(sessionRaw), &session) != nil || session.ID != verificationID || session.Version != version || verification.ValidateSession(session) != nil || session.CreatedAt.After(a.Now) || session.UpdatedAt.After(a.Now) || !projectCompletionVerificationBinding(b, session.Binding) || session.State != verification.VerifiedComplete || verification.Evaluate(session, session.Binding, a.Now) != verification.VerifiedComplete || envelope.Bundle.VerificationID != session.ID || envelope.Bundle.CreatedAt.After(a.Now) {
		return zero, verification.ErrBindingMismatch
	}
	if err = envelope.Bundle.Verify(envelope.Payloads, session.Binding); err != nil {
		return zero, err
	}
	input := ProjectArchitectureReviewInput{Binding: session.Binding, VerificationID: session.ID, VerificationVersion: session.Version, Contract: ref, PlanDigest: projectcontext.Digest("marshal.architecture.plan.v1", p), SessionDigest: projectcontext.Digest("marshal.architecture.verification.v1", session), BundleDigest: envelope.Bundle.ManifestDigest, Packet: packet}
	input.Obligations = []string{"purpose"}
	input.Obligations = append(input.Obligations, "goal:outcome")
	for i, constraint := range goal.Constraints {
		input.Obligations = append(input.Obligations, fmt.Sprintf("goal:constraint:%d:%s", i, constraint.ID))
	}
	for i := range goal.DoNotDo {
		input.Obligations = append(input.Obligations, fmt.Sprintf("goal:do_not_do:%d", i))
	}
	for i := range goal.SuccessCriteria {
		input.Obligations = append(input.Obligations, fmt.Sprintf("goal:criterion:%d", i))
	}
	for i := range goal.RequiredCriticalClaims {
		input.Obligations = append(input.Obligations, fmt.Sprintf("goal:critical_claim:%d", i))
	}
	if goal.ExpectedArtifact != "" {
		input.Obligations = append(input.Obligations, "goal:artifact")
	}
	for i := range goal.Scope {
		input.Obligations = append(input.Obligations, fmt.Sprintf("goal:scope:%d", i))
	}
	for _, component := range contract.Components {
		input.Obligations = append(input.Obligations, "component:"+component.ID)
	}
	for i := range contract.Invariants {
		input.Obligations = append(input.Obligations, fmt.Sprintf("invariant:%d", i))
	}
	for i := range contract.ProhibitedChanges {
		input.Obligations = append(input.Obligations, fmt.Sprintf("prohibited:%d", i))
	}
	for i := range contract.SourceAnchors {
		input.Obligations = append(input.Obligations, fmt.Sprintf("source_anchor:%d", i))
	}
	for i := range contract.DecisionRefs {
		input.Obligations = append(input.Obligations, fmt.Sprintf("decision_ref:%d", i))
	}
	for _, ref := range contract.Required {
		if ref.Mandatory() {
			input.Obligations = append(input.Obligations, "memory:"+ref.ID)
		}
	}
	for _, task := range p.Tasks {
		input.Obligations = append(input.Obligations, "task:"+task.ID)
	}
	sort.Strings(input.Obligations)
	if len(input.Obligations) > 1024 {
		return zero, projectcontext.ErrBudget
	}
	input.EvidenceRefs = []string{"workspace:" + b.Tree, "bundle:" + envelope.Bundle.ManifestDigest, fmt.Sprintf("verification:%s:%d", session.ID, session.Version)}
	for _, e := range session.Evidence {
		input.EvidenceRefs = append(input.EvidenceRefs, "evidence:"+e.ID)
	}
	for _, entry := range envelope.Bundle.Entries {
		input.EvidenceRefs = append(input.EvidenceRefs, "bundle-entry:"+entry.Path+":"+entry.Digest)
	}
	sort.Strings(input.EvidenceRefs)
	boundInput := input
	boundInput.Packet.Text = "" // Content is already bound by packet/manifest SHA.
	input.Digest = projectcontext.Digest("marshal.project.architecture.input.v1", boundInput)
	return input, nil
}

// architectureFileLine matches a reviewer's citation of a file line or line
// range in the reviewed workspace, such as calc.py:2 or pkg/x.go:10-14.
var architectureFileLine = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*\.[A-Za-z0-9]+:[0-9]+(-[0-9]+)?$`)

func projectArchitectureOutcome(input ProjectArchitectureReviewInput, observation ProjectArchitectureObservation) (string, error) {
	if len(observation.Assessments) != len(input.Obligations) {
		return "", projectcontext.Changed("project_context_architecture_extreme.go:327")
	}
	obligations := map[string]bool{}
	for _, id := range input.Obligations {
		if obligations[id] {
			return "", projectcontext.Changed("project_context_architecture_extreme.go:332")
		}
		obligations[id] = true
	}
	evidence := map[string]bool{}
	for _, ref := range input.EvidenceRefs {
		evidence[ref] = true
	}
	evidence["result:"+observation.Invocation.SourceHead] = true
	workspaceOffered := false
	for _, ref := range input.EvidenceRefs {
		workspaceOffered = workspaceOffered || strings.HasPrefix(ref, "workspace:")
	}
	seen := map[string]bool{}
	outcome := "COMPATIBLE"
	for _, assessment := range observation.Assessments {
		if !obligations[assessment.ObligationID] || seen[assessment.ObligationID] || strings.TrimSpace(assessment.Reason) == "" || len(assessment.Reason) > 2048 || !utf8.ValidString(assessment.Reason) || strings.ContainsRune(assessment.Reason, 0) || len(assessment.EvidenceRefs) > 64 {
			return "", projectcontext.Changed("project_context_architecture_extreme.go:345")
		}
		seen[assessment.ObligationID] = true
		// The assessment is bound to its obligation by obligation_id. Beside
		// the offered references a reviewer may also cite file lines
		// ("calc.py:2") or repeat one; those are ignored. An assessment with
		// no concrete offered reference at all proves nothing and refuses.
		concrete := false
		for _, ref := range assessment.EvidenceRefs {
			// A file-line citation ("calc.py:2") points into the bound
			// workspace the reviewer was given, so it counts when that
			// workspace is among the offered references.
			if evidence[ref] || workspaceOffered && architectureFileLine.MatchString(ref) {
				concrete = true
				break
			}
		}
		if !concrete {
			return "", projectcontext.Changed("project_context_architecture_extreme.go: no offered evidence for " + assessment.ObligationID)
		}
		switch assessment.Status {
		case "PASS":
		case "FAIL":
			outcome = "INCOMPATIBLE"
		case "UNKNOWN":
			if outcome != "INCOMPATIBLE" {
				outcome = "UNKNOWN"
			}
		case "N/A":
			if outcome == "COMPATIBLE" {
				outcome = "NOT_APPLICABLE"
			}
		default:
			return "", projectcontext.ErrInvalid
		}
	}
	if outcome == "COMPATIBLE" && (projectArchitectureUnknownIdentity(observation.Invocation.ObservedModel) || projectArchitectureUnknownIdentity(observation.Invocation.SessionID)) {
		outcome = "UNKNOWN"
	}
	return outcome, nil
}

func projectArchitectureUnknownIdentity(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "unknown", "n/a", "unavailable", "unresolved", "none":
		return true
	}
	return false
}
func projectArchitectureSessionKey(inv ProjectArchitectureInvocation) string {
	return "architecture-session-" + projectcontext.Digest("marshal.architecture.session.v1", struct{ Provider, Session string }{inv.Provider, inv.SessionID})[7:]
}

func projectArchitectureTree(s string) bool {
	if len(s) != 64 || s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func projectArchitectureObject(s string) bool {
	if (len(s) != 40 && len(s) != 64) || s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
