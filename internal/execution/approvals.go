package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// ApprovalRequest holds data needed to request a runtime approval.
type ApprovalRequest struct {
	RunID          string
	TaskID         string
	PlanID         string
	PlanVersion    int64
	OperationType  string
	TargetResource string
	RiskLevel      model.Risk
	Scope          string
	DiffPreview    string
	Parameters     string
	CurrentState   string
	TTL            time.Duration
	Now            time.Time
}

// ComputeActionDigest creates a deterministic SHA256 digest of the exact action parameters.
func ComputeActionDigest(opType, target, diff, params string) string {
	h := sha256.New()
	fmt.Fprintf(h, "op:%s\n", opType)
	fmt.Fprintf(h, "target:%s\n", target)
	fmt.Fprintf(h, "diff:%s\n", diff)
	fmt.Fprintf(h, "params:%s\n", params)
	return hex.EncodeToString(h.Sum(nil))
}

// ComputeStateDigest creates a SHA256 digest of current state representation.
func ComputeStateDigest(state string) string {
	h := sha256.New()
	fmt.Fprintf(h, "state:%s\n", state)
	return hex.EncodeToString(h.Sum(nil))
}

// ApprovalManager manages runtime human approval gates with TOCTOU guarantees.
type ApprovalManager struct {
	mu        sync.RWMutex
	dir       string
	approvals map[string]*RuntimeApproval // approvalID -> RuntimeApproval
}

// NewApprovalManager creates a new runtime approval manager.
func NewApprovalManager(storageDir ...string) *ApprovalManager {
	dir := ""
	if len(storageDir) > 0 && storageDir[0] != "" {
		dir = storageDir[0]
		_ = os.MkdirAll(dir, 0755)
	}
	return &ApprovalManager{
		dir:       dir,
		approvals: make(map[string]*RuntimeApproval),
	}
}

// RequestApproval registers a new approval requirement.
func (am *ApprovalManager) RequestApproval(req ApprovalRequest) (*RuntimeApproval, error) {
	if req.RunID == "" || req.TaskID == "" || req.OperationType == "" {
		return nil, fmt.Errorf("%w: missing required approval parameters", ErrRunInvalid)
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	ttl := req.TTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	expiresAt := now.Add(ttl)

	approvalID := fmt.Sprintf("app-%s-%s-%d", req.RunID, req.TaskID, now.UnixNano())
	actionDigest := ComputeActionDigest(req.OperationType, req.TargetResource, req.DiffPreview, req.Parameters)
	stateDigest := ComputeStateDigest(req.CurrentState)

	approval := &RuntimeApproval{
		ApprovalID:     approvalID,
		RunID:          req.RunID,
		TaskID:         req.TaskID,
		PlanID:         req.PlanID,
		PlanVersion:    req.PlanVersion,
		OperationType:  req.OperationType,
		TargetResource: req.TargetResource,
		RiskLevel:      req.RiskLevel,
		Scope:          req.Scope,
		DiffPreview:    req.DiffPreview,
		ActionDigest:   actionDigest,
		StateDigest:    stateDigest,
		Status:         ApprovalRequested,
		CreatedAt:      now,
		ExpiresAt:      &expiresAt,
	}

	am.approvals[approvalID] = approval
	if am.dir != "" {
		data, _ := json.MarshalIndent(approval, "", "  ")
		_ = os.WriteFile(filepath.Join(am.dir, approvalID+".json"), data, 0644)
	}
	return approval, nil
}

// Decide processes a human decision (Approve or Deny).
func (am *ApprovalManager) Decide(approvalID string, approve bool, decider, reason string, now time.Time) (*RuntimeApproval, error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	app, exists := am.approvals[approvalID]
	if !exists && am.dir != "" {
		data, err := os.ReadFile(filepath.Join(am.dir, approvalID+".json"))
		if err == nil {
			var loaded RuntimeApproval
			if err := json.Unmarshal(data, &loaded); err == nil {
				app = &loaded
				am.approvals[approvalID] = app
				exists = true
			}
		}
	}
	if !exists {
		return nil, fmt.Errorf("approval %s not found", approvalID)
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	if app.ExpiresAt != nil && now.After(*app.ExpiresAt) {
		app.Status = ApprovalExpired
		return app, fmt.Errorf("%w: approval expired", ErrApprovalTOCTOUViolation)
	}

	if app.Status != ApprovalRequested {
		return app, fmt.Errorf("%w: cannot decide on approval in state %s", ErrInvalidStateTransition, app.Status)
	}

	app.ResolvedAt = &now
	app.ApprovedBy = decider
	app.DecisionReason = reason

	if approve {
		app.Status = ApprovalApproved
	} else {
		app.Status = ApprovalDenied
	}

	if am.dir != "" {
		data, _ := json.MarshalIndent(app, "", "  ")
		_ = os.WriteFile(filepath.Join(am.dir, approvalID+".json"), data, 0644)
	}

	return app, nil
}

// Approve records an approval decision.
func (am *ApprovalManager) Approve(approvalID, decider, reason string, now time.Time) error {
	_, err := am.Decide(approvalID, true, decider, reason, now)
	return err
}

// Deny records a rejection decision.
func (am *ApprovalManager) Deny(approvalID, decider, reason string, now time.Time) error {
	_, err := am.Decide(approvalID, false, decider, reason, now)
	return err
}

// ValidateAndConsume validates the approval against current action digest and consumes it (one-shot).
func (am *ApprovalManager) ValidateAndConsume(approvalID, currentActionDigest, currentStateDigest string, now time.Time) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	app, exists := am.approvals[approvalID]
	if !exists && am.dir != "" {
		data, err := os.ReadFile(filepath.Join(am.dir, approvalID+".json"))
		if err == nil {
			var loaded RuntimeApproval
			if err := json.Unmarshal(data, &loaded); err == nil {
				app = &loaded
				am.approvals[approvalID] = app
				exists = true
			}
		}
	}
	if !exists {
		return fmt.Errorf("approval %s not found", approvalID)
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	if app.ExpiresAt != nil && now.After(*app.ExpiresAt) {
		app.Status = ApprovalExpired
		if am.dir != "" {
			data, _ := json.MarshalIndent(app, "", "  ")
			_ = os.WriteFile(filepath.Join(am.dir, approvalID+".json"), data, 0644)
		}
		return fmt.Errorf("%w: approval expired at %s", ErrApprovalTOCTOUViolation, app.ExpiresAt)
	}

	if app.Status != ApprovalApproved {
		if app.Status == ApprovalDenied {
			return ErrApprovalDenied
		}
		return fmt.Errorf("%w: approval status is %s, required APPROVED", ErrApprovalRequired, app.Status)
	}

	// Exact action check: prevents TOCTOU mutation of approved action
	if app.ActionDigest != currentActionDigest {
		app.Status = ApprovalInvalidated
		if am.dir != "" {
			data, _ := json.MarshalIndent(app, "", "  ")
			_ = os.WriteFile(filepath.Join(am.dir, approvalID+".json"), data, 0644)
		}
		return fmt.Errorf("%w: action digest mismatch (approved %s, current %s)",
			ErrApprovalTOCTOUViolation, app.ActionDigest, currentActionDigest)
	}

	// State check if provided
	if currentStateDigest != "" && app.StateDigest != "" && app.StateDigest != currentStateDigest {
		app.Status = ApprovalInvalidated
		if am.dir != "" {
			data, _ := json.MarshalIndent(app, "", "  ")
			_ = os.WriteFile(filepath.Join(am.dir, approvalID+".json"), data, 0644)
		}
		return fmt.Errorf("%w: state digest mismatch since approval was given", ErrApprovalTOCTOUViolation)
	}

	// One-shot consumption: Once validated and consumed, mark invalidated/consumed so it cannot be reused
	app.Status = ApprovalInvalidated
	if am.dir != "" {
		data, _ := json.MarshalIndent(app, "", "  ")
		_ = os.WriteFile(filepath.Join(am.dir, approvalID+".json"), data, 0644)
	}
	return nil
}

// GetApproval retrieves an approval by ID.
func (am *ApprovalManager) GetApproval(approvalID string) (*RuntimeApproval, error) {
	am.mu.RLock()
	app, exists := am.approvals[approvalID]
	am.mu.RUnlock()

	if !exists && am.dir != "" {
		data, err := os.ReadFile(filepath.Join(am.dir, approvalID+".json"))
		if err == nil {
			var loaded RuntimeApproval
			if err := json.Unmarshal(data, &loaded); err == nil {
				am.mu.Lock()
				am.approvals[approvalID] = &loaded
				app = &loaded
				exists = true
				am.mu.Unlock()
			}
		}
	}
	if !exists {
		return nil, fmt.Errorf("approval %s not found", approvalID)
	}
	return app, nil
}
