// Package lifecycle exposes a narrow, typed, local-process facade for the
// canonical MARSHAL Process03→07 services. It deliberately contains no
// network listener, shell command, provider credential, or Enterprise logic.
package lifecycle

import (
	"context"
	"fmt"
	"sync"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/verification"
)

// Request contains only typed lifecycle inputs. CorrelationID is an
// idempotency key supplied by a local caller; it is never executable text.
type Request struct {
	CorrelationID        string
	SessionID            string
	GoalID               string
	ProjectID            string
	Intent               string
	Scope                []string
	Constraints          []model.Constraint
	DoNotDo              []string
	SuccessCriteria      []string
	Tasks                []plan.Task
	Candidates           []goalintake.Candidate
	HarnessCandidates    []plan.HarnessCandidate
	Role                 model.Role
	RequestedHarness     string
	ApprovalRequirements []string
	EvidenceRequirements []string
}

type Status string

const (
	StatusCompleted          Status = "COMPLETED"
	StatusApprovalRequired   Status = "APPROVAL_REQUIRED"
	StatusVerificationFailed Status = "VERIFICATION_FAILED"
	StatusFailed             Status = "FAILED"
)

type Result struct {
	Status             Status
	CorrelationID      string
	GoalID             string
	PlanID             string
	RunID              string
	VerificationID     string
	MemoryCommitID     string
	FailureFingerprint string
}

// ApprovalAuthority belongs to the local MARSHAL host. An Enterprise caller
// never supplies this implementation and therefore cannot self-approve.
type ApprovalAuthority interface {
	Approved(context.Context, Request) (bool, error)
}

// ExecutionApprovalAuthority is an optional local decision point for a
// canonical Process05 approval record. The adapter never invents approval: it
// passes the exact Request correlation to a local authority, then records the
// decision through ExecutionService.Approve.
type ExecutionApprovalAuthority interface {
	ApproveExecution(context.Context, Request, string) (bool, error)
}

// Verifier is supplied by the local MARSHAL host. It supplies independently
// gathered evidence, while the canonical verification service derives the
// immutable Process05 binding and makes the decision.
type Verifier interface {
	Session(context.Context, Request, verification.Binding) (verification.Session, error)
	Envelope(context.Context, Request, verification.Session) (verification.BundleEnvelope, string, error)
}

type Adapter struct {
	runtime   *app.Runtime
	approvals ApprovalAuthority
	verifier  Verifier
	mu        sync.Mutex
	completed map[string]Result
}

func New(runtime *app.Runtime, approvals ApprovalAuthority, verifier Verifier) (*Adapter, error) {
	if runtime == nil {
		return nil, fmt.Errorf("canonical runtime is required")
	}
	return &Adapter{runtime: runtime, approvals: approvals, verifier: verifier, completed: map[string]Result{}}, nil
}

// Submit persists the Process03 Goal and creates the canonical Process04
// plan. It never grants approval; callers receive APPROVAL_REQUIRED until the
// local authority has accepted the exact typed request.
func (a *Adapter) Submit(ctx context.Context, req Request) (Result, error) {
	if err := validate(req); err != nil {
		return Result{Status: StatusFailed, CorrelationID: req.CorrelationID, FailureFingerprint: "invalid-lifecycle-request"}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if prior, ok := a.completed[req.CorrelationID]; ok {
		return prior, nil
	}
	approved := false
	if a.approvals != nil {
		var err error
		approved, err = a.approvals.Approved(ctx, req)
		if err != nil {
			return Result{Status: StatusFailed, CorrelationID: req.CorrelationID, FailureFingerprint: "local-approval-error"}, err
		}
	}
	confirmation := model.ConfirmationPending
	if approved {
		confirmation = model.ConfirmationApproved
	}
	goal := model.GoalContract{ID: req.GoalID, SessionID: req.SessionID, ProjectID: req.ProjectID, OriginalRequest: req.Intent, ConstitutionVersion: constitution.Current.String(), Confirmation: confirmation, Revision: 1, DesiredOutcome: req.Intent, Scope: append([]string(nil), req.Scope...), Constraints: append([]model.Constraint(nil), req.Constraints...), DoNotDo: append([]string(nil), req.DoNotDo...), SuccessCriteria: append([]string(nil), req.SuccessCriteria...), Risk: model.R1, AuthoritySource: "local-lifecycle-adapter"}
	goal.EvaluateUnderstanding()
	if err := a.runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		return Result{Status: StatusFailed, CorrelationID: req.CorrelationID, FailureFingerprint: "process03-goal-persistence"}, err
	}
	planReq := app.CreatePlanRequest{SessionID: req.SessionID, ProjectID: projectid.ID(req.ProjectID), Tasks: append([]plan.Task(nil), req.Tasks...), Candidates: append([]goalintake.Candidate(nil), req.Candidates...), HarnessCandidates: append([]plan.HarnessCandidate(nil), req.HarnessCandidates...), RequiredCapabilities: []string{req.RequestedHarness}}
	p, err := a.runtime.Plans().Create(ctx, planReq)
	if err != nil {
		return Result{Status: StatusFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, FailureFingerprint: "process04-plan"}, err
	}
	result := Result{CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID}
	if !approved {
		result.Status = StatusApprovalRequired
		return result, nil
	}
	if _, err := a.runtime.Plans().Approve(ctx, projectid.ID(req.ProjectID)); err != nil {
		return Result{Status: StatusFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, FailureFingerprint: "process04-approval"}, err
	}
	run, err := a.runtime.Execution().StartRun(ctx, req.SessionID, projectid.ID(req.ProjectID))
	if err != nil {
		return Result{Status: StatusFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, FailureFingerprint: "process05-start"}, err
	}
	executed, err := a.runtime.Execution().ExecuteRun(ctx, run.RunID)
	if err != nil {
		return Result{Status: StatusFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, FailureFingerprint: "process05-execution"}, err
	}
	if executed.State == execution.RunNeedsApproval {
		approvalID := ""
		for _, task := range executed.Tasks {
			if task.ApprovalID != "" {
				approvalID = task.ApprovalID
				break
			}
		}
		local, ok := a.approvals.(ExecutionApprovalAuthority)
		if !ok || approvalID == "" {
			return Result{Status: StatusApprovalRequired, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID}, nil
		}
		allowed, approvalErr := local.ApproveExecution(ctx, req, approvalID)
		if approvalErr != nil {
			return Result{Status: StatusFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, FailureFingerprint: "process05-approval-error"}, approvalErr
		}
		if !allowed {
			return Result{Status: StatusApprovalRequired, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID}, nil
		}
		if err := a.runtime.Execution().Approve(ctx, approvalID, "local-lifecycle-authority", "locally approved typed lifecycle request "+req.CorrelationID); err != nil {
			return Result{Status: StatusFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, FailureFingerprint: "process05-approval"}, err
		}
		executed, err = a.runtime.Execution().ExecuteRun(ctx, run.RunID)
		if err != nil {
			return Result{Status: StatusFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, FailureFingerprint: "process05-resume"}, err
		}
	}
	if executed.State != execution.RunDonePendingVerification {
		return Result{Status: StatusFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, FailureFingerprint: "process05-terminal-state"}, fmt.Errorf("Process05 finished in %s", executed.State)
	}
	if a.verifier == nil {
		return Result{Status: StatusVerificationFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, FailureFingerprint: "no-local-verifier"}, fmt.Errorf("local Process06 verifier is required")
	}
	binding, err := a.runtime.Verification().BindingForRun(ctx, run.RunID)
	if err != nil {
		return Result{Status: StatusVerificationFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, FailureFingerprint: "process06-binding"}, err
	}
	session, err := a.verifier.Session(ctx, req, binding)
	if err != nil {
		return Result{Status: StatusVerificationFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, FailureFingerprint: "process06-evidence"}, err
	}
	started, err := a.runtime.Verification().StartForRun(ctx, run.RunID, session)
	if err != nil {
		return Result{Status: StatusVerificationFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, FailureFingerprint: "process06-start"}, err
	}
	verified, err := a.runtime.Verification().Evaluate(ctx, started.ID)
	if err != nil || verified.State != verification.VerifiedComplete {
		if err == nil {
			err = fmt.Errorf("verification decision %s", verified.State)
		}
		return Result{Status: StatusVerificationFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, VerificationID: started.ID, FailureFingerprint: "process06-decision"}, err
	}
	envelope, provenance, err := a.verifier.Envelope(ctx, req, verified)
	if err != nil {
		return Result{Status: StatusVerificationFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, VerificationID: started.ID, FailureFingerprint: "process06-evidence"}, err
	}
	attestation, err := a.runtime.Verification().Attest(ctx, started.ID, envelope, provenance)
	if err != nil {
		return Result{Status: StatusVerificationFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, VerificationID: started.ID, FailureFingerprint: "process06-attestation"}, err
	}
	commit, err := a.runtime.Learning().Commit(ctx, app.CommitInput{ID: "learning-" + req.CorrelationID, Verification: started.ID, Provenance: attestation.ID})
	if err != nil {
		return Result{Status: StatusFailed, CorrelationID: req.CorrelationID, GoalID: goal.ID, PlanID: p.ID, RunID: run.RunID, VerificationID: started.ID, FailureFingerprint: "process07-learning"}, err
	}
	result.Status = StatusCompleted
	result.RunID = run.RunID
	result.VerificationID = started.ID
	result.MemoryCommitID = commit.ID
	a.completed[req.CorrelationID] = result
	return result, nil
}
func validate(r Request) error {
	if r.CorrelationID == "" || r.SessionID == "" || r.GoalID == "" || r.ProjectID == "" || r.Intent == "" || r.RequestedHarness == "" || len(r.Constraints) == 0 || len(r.Tasks) == 0 {
		return fmt.Errorf("typed lifecycle correlation, goal, project, intent, constraints, tasks, and harness are required")
	}
	for _, c := range r.Constraints {
		if !c.IsHard || c.Text == "" {
			return fmt.Errorf("immutable constraints must be nonempty and hard")
		}
	}
	return nil
}
