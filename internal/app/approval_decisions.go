package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

// DecisionRecord names the canonical kind and exact revision displayed to the
// owner. Digest binds the entire record, including action, state and expiry.
type DecisionRecord struct {
	ID, Kind, ResourceID, Status, Digest, RequestedBy string
	Version                                           int64
	Pending                                           bool
	ExpiresAt                                         *time.Time
	goal                                              *model.GoalContract
	plan                                              *plan.ExecutionPlan
	approval                                          *model.Approval
	execution                                         *execution.RuntimeApproval
}
type ApprovalDecision struct {
	Envelope CommandEnvelope
	Digest   string
	Approve  bool
	Reason   string
}

func decisionDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func goalDecision(g model.GoalContract) DecisionRecord {
	return DecisionRecord{ID: fmt.Sprintf("goal:%s@%d", g.ID, g.Revision), Kind: "goal", ResourceID: g.ID, Version: g.Revision, Status: string(g.Confirmation), Pending: g.Confirmation == model.ConfirmationPending, Digest: decisionDigest(g), goal: &g}
}
func planDecision(p plan.ExecutionPlan) DecisionRecord {
	return DecisionRecord{ID: fmt.Sprintf("plan:%s@%d", p.ID, p.Version), Kind: "plan", ResourceID: p.ID, Version: p.Version, Status: string(p.State), Pending: p.State == plan.StateReady, Digest: decisionDigest(p), plan: &p}
}
func sqliteDecision(a model.Approval) DecisionRecord {
	return DecisionRecord{ID: "approval:" + a.ID, Kind: "approval", ResourceID: a.ID, Version: a.Revision, Status: string(a.Status), Pending: a.Status == model.ApprovalRequested, Digest: decisionDigest(a), RequestedBy: a.RequestedBy, ExpiresAt: a.ExpiresAt, approval: &a}
}
func executionDecision(a execution.RuntimeApproval) DecisionRecord {
	return DecisionRecord{ID: "execution:" + a.ApprovalID, Kind: "execution", ResourceID: a.ApprovalID, Version: a.Revision, Status: string(a.Status), Pending: a.Status == execution.ApprovalRequested, Digest: decisionDigest(a), RequestedBy: a.RequestedBy, ExpiresAt: a.ExpiresAt, execution: &a}
}

// DecisionRecords aggregates canonical records within the project/session.
func (r *Runtime) DecisionRecords(ctx context.Context, session string, pending bool) ([]DecisionRecord, error) {
	if r == nil || r.store == nil {
		return nil, model.ErrUnavailable
	}
	var records []DecisionRecord
	add := func(record DecisionRecord) {
		if record.Pending == pending {
			records = append(records, record)
		}
	}
	if g, err := r.store.GetActiveGoalContract(ctx, session); err == nil {
		if !r.decisionProjectMatches(g.ProjectID) {
			return nil, authz.ErrDenied
		}
		if g.Confirmation != model.ConfirmationNeedsInput {
			add(goalDecision(g))
		}
	} else if !errors.Is(err, model.ErrGoalNotFound) {
		return nil, err
	}
	projects := []string{r.ProjectIdentity()}
	if r.ProjectID() != r.ProjectIdentity() {
		projects = append(projects, r.ProjectID())
	}
	for _, project := range projects {
		if p, err := r.Plans().Current(ctx, projectid.ID(project)); err == nil && r.planBelongsToSession(ctx, p, session) && (p.State == plan.StateReady || p.State == plan.StateApproved || p.State == plan.StateCancelled) {
			add(planDecision(p))
		}
	}

	var approvals []model.Approval
	var err error
	if pending {
		approvals, err = r.store.ListPendingApprovals(ctx, r.ProjectIdentity())
	} else {
		approvals, err = r.store.ListResolvedApprovals(ctx, r.ProjectIdentity(), 50)
	}
	if err != nil {
		return nil, err
	}
	if r.ProjectID() != r.ProjectIdentity() {
		var legacy []model.Approval
		if pending {
			legacy, err = r.store.ListPendingApprovals(ctx, r.ProjectID())
		} else {
			legacy, err = r.store.ListResolvedApprovals(ctx, r.ProjectID(), 50)
		}
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, legacy...)
	}
	for _, a := range approvals {
		add(sqliteDecision(a))
	}
	service := r.Execution()
	runs, err := service.ListRuns(ctx)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, run := range runs {
		if run.SessionID == session && r.decisionProjectMatches(string(run.ProjectID)) {
			allowed[run.RunID] = true
		}
	}
	all, err := service.Engine().ApprovalManager().ListApprovals()
	if err != nil {
		return nil, err
	}
	for _, a := range all {
		if allowed[a.RunID] {
			add(executionDecision(a))
		}
	}
	return records, nil
}

// ResolveDecision refuses ambiguous bare IDs and lists exact typed candidates.
// It does not select the first pending item or infer a kind from an ID prefix.
func (r *Runtime) ResolveDecision(ctx context.Context, session, id string) (DecisionRecord, error) {
	if r == nil || r.store == nil {
		return DecisionRecord{}, model.ErrUnavailable
	}
	if kind, rest, typed := strings.Cut(id, ":"); typed {
		switch kind {
		case "goal", "plan":
			raw, version, ok := strings.Cut(rest, "@")
			n, err := strconv.ParseInt(version, 10, 64)
			if !ok || err != nil || n < 1 {
				return DecisionRecord{}, model.ErrInvalid
			}
			if kind == "goal" {
				g, err := r.store.GetGoalContract(ctx, raw, n)
				if err != nil {
					return DecisionRecord{}, err
				}
				if g.SessionID != session || !r.decisionProjectMatches(g.ProjectID) {
					return DecisionRecord{}, authz.ErrDenied
				}
				return goalDecision(g), nil
			}
			p, err := r.store.GetPlan(ctx, raw, n)
			if err != nil {
				return DecisionRecord{}, err
			}
			if !r.decisionProjectMatches(string(p.ProjectID)) || !r.planBelongsToSession(ctx, p, session) {
				return DecisionRecord{}, authz.ErrDenied
			}
			return planDecision(p), nil
		case "approval":
			a, err := r.store.GetApproval(ctx, rest)
			if err != nil {
				return DecisionRecord{}, err
			}
			if !r.decisionProjectMatches(a.ProjectID) {
				return DecisionRecord{}, authz.ErrDenied
			}
			return sqliteDecision(a), nil
		case "execution":
			a, err := r.Execution().Engine().ApprovalManager().GetApproval(rest)
			if err != nil {
				return DecisionRecord{}, err
			}
			run, err := r.Execution().GetRun(ctx, a.RunID)
			if err != nil {
				return DecisionRecord{}, err
			}
			if run.SessionID != session || !r.decisionProjectMatches(string(run.ProjectID)) {
				return DecisionRecord{}, authz.ErrDenied
			}
			return executionDecision(*a), nil
		default:
			return DecisionRecord{}, model.ErrInvalid
		}
	}
	var matches []DecisionRecord
	for _, pending := range []bool{true, false} {
		records, err := r.DecisionRecords(ctx, session, pending)
		if err != nil {
			return DecisionRecord{}, err
		}
		for _, record := range records {
			if record.ResourceID == id {
				matches = append(matches, record)
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	candidates := []string{}
	for _, record := range matches {
		candidates = append(candidates, record.ID)
	}
	if len(matches) > 1 {
		return DecisionRecord{}, fmt.Errorf("%w: ambiguous id %s; candidates: %s", model.ErrConflict, id, strings.Join(candidates, ", "))
	}
	return DecisionRecord{}, model.ErrNotFound
}

// CommandDecideApproval is the authenticated operator boundary. CI-003 forbids
// action requesters from deciding their own actions. Goal and plan confirmations
// are the owner accepting MARSHAL's interpretation, so owner creation provenance
// is not an action requester. Agents cannot acquire this Runtime-bound context.
func (r *Runtime) CommandDecideApproval(ctx context.Context, request ApprovalDecision) (DecisionRecord, error) {
	e := request.Envelope
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID || e.ProjectID != r.ProjectIdentity() {
		return DecisionRecord{}, authz.ErrDenied
	}
	if e.SessionID == "" || e.TargetID == "" || e.ExpectedVersion < 0 || strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 || request.Digest == "" {
		return DecisionRecord{}, model.ErrInvalid
	}
	subject := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: "approval.decide"}
	grant, err := authz.CanWithCapability(ctx, subject, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(r.store, nil))
	if err != nil {
		return DecisionRecord{}, err
	}
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	record := store.CommandRecord{ProjectID: e.ProjectID, Actor: p.ID(), Key: hex.EncodeToString(key[:]), Operation: "approval.decide", SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Digest: decisionDigest(request), CapabilityGrantID: grant.CapabilityGrantID}
	if version, found, err := r.store.CommandResult(ctx, record); err != nil {
		return DecisionRecord{}, err
	} else if found {
		return r.readDecisionResult(ctx, e, version)
	}
	current, err := r.ResolveDecision(ctx, e.SessionID, e.TargetID)
	if err != nil {
		return DecisionRecord{}, err
	}
	if current.ID != e.TargetID || (current.Kind != "execution" && (current.Version != e.ExpectedVersion || current.Digest != request.Digest)) {
		return DecisionRecord{}, fmt.Errorf("%w: stale approval binding", model.ErrConflict)
	}
	if current.Kind == "execution" {
		return r.commandExecutionDecision(ctx, request, record, current, p.ID())
	}
	if request.Approve && current.approval != nil {
		for _, condition := range current.approval.Conditions {
			if inv, found := constitution.Default().Lookup(constitution.InvariantID(condition)); found && inv.Severity == constitution.SeverityHard {
				return DecisionRecord{}, fmt.Errorf("%w: hard violation %s cannot be approved", authz.ErrDenied, condition)
			}
		}
	}
	if !current.Pending {
		return DecisionRecord{}, fmt.Errorf("%w: decision already %s", model.ErrConflict, current.Status)
	}
	if current.ExpiresAt != nil && !time.Now().UTC().Before(*current.ExpiresAt) {
		return DecisionRecord{}, fmt.Errorf("%w: approval expired", model.ErrApprovalRequired)
	}
	if (current.Kind == "approval" || current.Kind == "execution") && current.RequestedBy == p.ID() {
		return DecisionRecord{}, fmt.Errorf("%w: requester cannot decide its own action (CI-003)", authz.ErrDenied)
	}
	// Older local goals/plans may carry Runtime.ProjectID(), the durable
	// project-row ID. Both IDs are derived from this Runtime's own project;
	// the envelope and capability always bind the canonical filesystem identity.
	if current.goal != nil {
		record.TargetProjectID = current.goal.ProjectID
	}
	if current.plan != nil {
		record.TargetProjectID = string(current.plan.ProjectID)
	}
	if current.approval != nil {
		record.TargetProjectID = current.approval.ProjectID
		record.ExpectedStateDigest = request.Digest
	}
	ctx = store.WithCommand(ctx, record)
	switch current.Kind {
	case "goal":
		active, err := r.store.GetActiveGoalContract(ctx, e.SessionID)
		if err != nil {
			return DecisionRecord{}, err
		}
		if active.ID != current.ResourceID || active.Revision != current.Version {
			return DecisionRecord{}, model.ErrGoalConflict
		}
		var g model.GoalContract
		if request.Approve {
			g, err = r.ApproveGoal(ctx, e.SessionID, current.Version)
		} else {
			g, err = r.RejectGoal(ctx, e.SessionID, current.Version, request.Reason)
		}
		if err != nil {
			return DecisionRecord{}, err
		}
		return goalDecision(g), nil
	case "plan":
		active, err := r.Plans().Current(ctx, current.plan.ProjectID)
		if err != nil {
			return DecisionRecord{}, err
		}
		if active.ID != current.ResourceID || active.Version != current.Version {
			return DecisionRecord{}, model.ErrConflict
		}
		goal, err := r.store.GetActiveGoalContract(ctx, e.SessionID)
		if err != nil {
			return DecisionRecord{}, err
		}
		if goal.ID != active.Goal.GoalID || goal.Revision != active.Goal.Revision {
			return DecisionRecord{}, model.ErrGoalConflict
		}
		var planResult plan.ExecutionPlan
		if request.Approve {
			planResult, err = r.Plans().ApproveVersion(ctx, current.plan.ProjectID, current.Version)
		} else {
			planResult, err = r.Plans().CancelVersion(ctx, current.plan.ProjectID, current.Version)
		}
		if err != nil {
			return DecisionRecord{}, err
		}
		return planDecision(planResult), nil
	case "approval":
		expiry := current.ExpiresAt
		if request.Approve && expiry == nil {
			deadline := time.Now().UTC().Add(15 * time.Minute)
			expiry = &deadline
		}
		a, err := r.store.ResolveApproval(ctx, current.ResourceID, p.ID(), request.Approve, expiry, current.Version)
		if err != nil {
			return DecisionRecord{}, err
		}
		return sqliteDecision(a), nil
	}
	return DecisionRecord{}, model.ErrInvalid
}
func (r *Runtime) readDecisionResult(ctx context.Context, e CommandEnvelope, version int64) (DecisionRecord, error) {
	kind, rest, _ := strings.Cut(e.TargetID, ":")
	if kind == "goal" || kind == "plan" {
		raw, _, _ := strings.Cut(rest, "@")
		return r.ResolveDecision(ctx, e.SessionID, fmt.Sprintf("%s:%s@%d", kind, raw, version))
	}
	return r.ResolveDecision(ctx, e.SessionID, e.TargetID)
}

func (r *Runtime) planBelongsToSession(ctx context.Context, p plan.ExecutionPlan, session string) bool {
	g, err := r.store.GetGoalContract(ctx, p.Goal.GoalID, p.Goal.Revision)
	return err == nil && g.SessionID == session
}

func (r *Runtime) commandExecutionDecision(ctx context.Context, request ApprovalDecision, record store.CommandRecord, current DecisionRecord, actor string) (DecisionRecord, error) {
	a := current.execution
	intent := record
	intent.Key += ":intent"
	intent.Operation = "approval.decide.intent"
	_, prepared, err := r.store.CommandResult(ctx, intent)
	if err != nil {
		return DecisionRecord{}, err
	}
	if prepared && a.DecisionCommandKey == record.Key && a.ApprovedBy == actor && ((request.Approve && (a.Status == execution.ApprovalApproved || a.Status == execution.ApprovalConsumed)) || (!request.Approve && a.Status == execution.ApprovalDenied)) {
		record.ResultVersion = current.Version
		if err := r.store.PersistDecisionCommand(ctx, record, "applied"); err != nil {
			return DecisionRecord{}, err
		}
		return r.ResolveDecision(ctx, record.SessionID, record.TargetID)
	}
	if !current.Pending || current.Version != request.Envelope.ExpectedVersion {
		return DecisionRecord{}, model.ErrConflict
	}
	if current.Digest != request.Digest {
		return DecisionRecord{}, execution.ErrApprovalTOCTOUViolation
	}
	requester := a.RequestedBy
	if requester == "" {
		requester = a.RunID
	}
	if requester == actor {
		return DecisionRecord{}, authz.ErrDenied
	}
	if request.Approve && a.HardViolation {
		return DecisionRecord{}, execution.ErrConstraintViolation
	}
	if a.ExpiresAt == nil || !time.Now().UTC().Before(*a.ExpiresAt) {
		return DecisionRecord{}, execution.ErrApprovalTOCTOUViolation
	}
	run, err := r.Execution().GetRun(ctx, a.RunID)
	if err != nil {
		return DecisionRecord{}, err
	}
	if err := r.Execution().Engine().ValidateRunGoal(ctx, run); err != nil {
		return DecisionRecord{}, err
	}
	if !prepared {
		if err := r.store.PersistDecisionCommand(ctx, intent, "prepared"); err != nil {
			return DecisionRecord{}, err
		}
	}
	ctx = execution.WithApprovalDecision(ctx, request.Digest, record.Key)
	if request.Approve {
		err = r.Execution().Approve(ctx, a.ApprovalID, actor, request.Reason)
	} else {
		err = r.Execution().Reject(ctx, a.ApprovalID, actor, request.Reason)
	}
	if err != nil {
		return DecisionRecord{}, err
	}
	after, err := r.ResolveDecision(ctx, record.SessionID, record.TargetID)
	if err != nil {
		return DecisionRecord{}, err
	}
	if after.execution.DecisionCommandKey != record.Key || after.execution.ApprovedBy != actor {
		return DecisionRecord{}, model.ErrConflict
	}
	record.ResultVersion = after.Version
	if err := r.store.PersistDecisionCommand(ctx, record, "applied"); err != nil {
		return DecisionRecord{}, err
	}
	return r.ResolveDecision(ctx, record.SessionID, record.TargetID)
}

func (r *Runtime) decisionProjectMatches(id string) bool {
	return id == r.ProjectIdentity() || id == r.ProjectID()
}
