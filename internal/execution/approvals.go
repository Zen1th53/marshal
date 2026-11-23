package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// ApprovalRequest holds data needed to request a runtime approval.
type ApprovalRequest struct {
	RequestedBy    string
	HardViolation  bool
	RunID          string
	TaskID         string
	PlanID         string
	PlanVersion    int64
	Attempt        int
	OperationType  string
	TargetResource string
	RiskLevel      model.Risk
	Scope          string
	ScopeDigest    string
	Authority      string
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

// ComputeScopeDigest creates a deterministic SHA256 digest of the bound approval scope.
func ComputeScopeDigest(planID string, planVersion int64, opType, target, scope, actionDigest string) string {
	h := sha256.New()
	fmt.Fprintf(h, "plan:%s@%d\n", planID, planVersion)
	fmt.Fprintf(h, "op:%s\n", opType)
	fmt.Fprintf(h, "target:%s\n", target)
	fmt.Fprintf(h, "scope:%s\n", scope)
	fmt.Fprintf(h, "action:%s\n", actionDigest)
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
	unlock, err := am.lockStorage()
	if err != nil {
		return nil, err
	}
	defer unlock()

	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	ttl := req.TTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	expiresAt := now.Add(ttl)

	approvalID := "app-" + uuid.NewString()
	actionDigest := ComputeActionDigest(req.OperationType, req.TargetResource, req.DiffPreview, req.Parameters)
	stateDigest := ComputeStateDigest(req.CurrentState)

	requester := req.RequestedBy
	if requester == "" {
		requester = req.RunID
	}
	scopeDigest := req.ScopeDigest
	if scopeDigest == "" {
		scopeDigest = ComputeScopeDigest(req.PlanID, req.PlanVersion, req.OperationType, req.TargetResource, req.Scope, actionDigest)
	}
	authority := req.Authority
	if authority == "" {
		authority = defaultTaskApprovalAuthority()
	}
	attempt := req.Attempt
	if attempt <= 0 {
		attempt = 1
	}
	approval := &RuntimeApproval{
		RequestedBy: requester, HardViolation: req.HardViolation, Revision: 1,
		ApprovalID:     approvalID,
		RunID:          req.RunID,
		TaskID:         req.TaskID,
		PlanID:         req.PlanID,
		PlanVersion:    req.PlanVersion,
		Attempt:        attempt,
		OperationType:  req.OperationType,
		TargetResource: req.TargetResource,
		RiskLevel:      req.RiskLevel,
		Scope:          req.Scope,
		ScopeDigest:    scopeDigest,
		Authority:      authority,
		DiffPreview:    req.DiffPreview,
		ActionDigest:   actionDigest,
		StateDigest:    stateDigest,
		Status:         ApprovalRequested,
		CreatedAt:      now,
		ExpiresAt:      &expiresAt,
	}

	if am.dir != "" {
		if err := am.persist(approval); err != nil {
			return nil, err
		}
	}
	am.approvals[approvalID] = approval
	copy := *approval
	return &copy, nil
}

// Decide processes a human decision (Approve or Deny).
type approvalDecisionBinding struct{ Digest, Key string }
type approvalDecisionKey struct{}

func WithApprovalDecision(ctx context.Context, digest, key string) context.Context {
	return context.WithValue(ctx, approvalDecisionKey{}, approvalDecisionBinding{digest, key})
}

func (am *ApprovalManager) Decide(approvalID string, approve bool, decider, reason string, now time.Time) (*RuntimeApproval, error) {
	return am.DecideContext(context.Background(), approvalID, approve, decider, reason, now)
}
func (am *ApprovalManager) DecideContext(ctx context.Context, approvalID string, approve bool, decider, reason string, now time.Time) (*RuntimeApproval, error) {
	am.mu.Lock()
	defer am.mu.Unlock()
	unlock, err := am.lockStorage()
	if err != nil {
		return nil, err
	}
	defer unlock()

	if err := am.refresh(approvalID); err != nil {
		return nil, err
	}
	app, exists := am.approvals[approvalID]

	if !exists {
		return nil, fmt.Errorf("approval %s not found", approvalID)
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	// Terminal or already-decided records are never overwritten by a late or
	// duplicate decision; return the existing receipt (including any
	// replacement pointer) unchanged.
	if app.Status != ApprovalRequested {
		copy := *app
		if app.Status == ApprovalExpired {
			return &copy, fmt.Errorf("%w: approval expired", ErrApprovalTOCTOUViolation)
		}
		if app.ReplacementID != "" {
			return &copy, fmt.Errorf("%w: cannot decide on approval in state %s (replacement %s)", ErrInvalidStateTransition, app.Status, app.ReplacementID)
		}
		return &copy, fmt.Errorf("%w: cannot decide on approval in state %s", ErrInvalidStateTransition, app.Status)
	}

	if app.ExpiresAt != nil && !now.Before(*app.ExpiresAt) {
		previous := *app
		app.Status = ApprovalExpired
		app.ResolvedAt = &now
		app.Revision++
		if am.dir != "" {
			if err := am.persist(app); err != nil {
				*app = previous
				return nil, err
			}
		}
		copy := *app
		return &copy, fmt.Errorf("%w: approval expired", ErrApprovalTOCTOUViolation)
	}

	requester := app.RequestedBy
	if requester == "" {
		requester = app.RunID
	}
	if decider == "" || requester == decider {
		return nil, fmt.Errorf("%w: requester cannot approve its own action", ErrApprovalDenied)
	}
	if reservedApprovalDecider(ctx, decider) {
		return nil, fmt.Errorf("%w: decider %s is reserved to the runtime", ErrApprovalDenied, decider)
	}
	if approve && app.HardViolation {
		return nil, fmt.Errorf("%w: hard violation cannot be approved", ErrConstraintViolation)
	}
	binding, bound := ctx.Value(approvalDecisionKey{}).(approvalDecisionBinding)
	if bound {
		raw, _ := json.Marshal(app)
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != binding.Digest {
			return nil, ErrApprovalTOCTOUViolation
		}
	}
	previous := *app
	if bound {
		app.DecisionCommandKey = binding.Key
	}
	app.Revision++
	app.ResolvedAt = &now
	app.ApprovedBy = decider
	app.DecisionReason = reason

	if approve {
		app.Status = ApprovalApproved
	} else {
		app.Status = ApprovalDenied
	}

	if am.dir != "" {
		if err := am.persist(app); err != nil {
			*app = previous
			return nil, err
		}
	}
	copy := *app
	return &copy, nil
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

// Expire atomically transitions an unanswered REQUESTED approval whose deadline
// has elapsed to EXPIRED. If another terminal transition or approval decision
// already won, it returns the existing state unchanged with transitioned=false.
func (am *ApprovalManager) Expire(approvalID string, now time.Time) (*RuntimeApproval, bool, error) {
	am.mu.Lock()
	defer am.mu.Unlock()
	unlock, err := am.lockStorage()
	if err != nil {
		return nil, false, err
	}
	defer unlock()

	if err := am.refresh(approvalID); err != nil {
		return nil, false, err
	}
	app, exists := am.approvals[approvalID]
	if !exists {
		return nil, false, fmt.Errorf("approval %s not found", approvalID)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if app.Status != ApprovalRequested || app.ExpiresAt == nil || now.Before(*app.ExpiresAt) {
		copy := *app
		return &copy, false, nil
	}
	previous := *app
	app.Status = ApprovalExpired
	app.ResolvedAt = &now
	app.Revision++
	if am.dir != "" {
		if err := am.persist(app); err != nil {
			*app = previous
			return nil, false, err
		}
	}
	copy := *app
	return &copy, true, nil
}

// Supersede atomically replaces a REQUESTED or unconsumed APPROVED approval
// with a stronger replacement ticket. If supersession wins after allow but
// before consume, the unconsumed grant is revoked under the same lock fence.
func (am *ApprovalManager) Supersede(approvalID, replacementID, reason string, now time.Time) (*RuntimeApproval, bool, error) {
	if strings.TrimSpace(replacementID) == "" || replacementID == approvalID {
		return nil, false, fmt.Errorf("%w: valid replacement approval id is required", ErrRunInvalid)
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	unlock, err := am.lockStorage()
	if err != nil {
		return nil, false, err
	}
	defer unlock()

	if err := am.refresh(approvalID); err != nil {
		return nil, false, err
	}
	app, exists := am.approvals[approvalID]
	if !exists {
		return nil, false, fmt.Errorf("approval %s not found", approvalID)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if app.Status != ApprovalRequested && app.Status != ApprovalApproved {
		copy := *app
		return &copy, false, nil
	}
	previous := *app
	wasApproved := app.Status == ApprovalApproved
	app.Status = ApprovalSuperseded
	app.ReplacementID = replacementID
	if app.ResolvedAt == nil {
		app.ResolvedAt = &now
	}
	if reason != "" {
		if wasApproved {
			app.RevokedReason = reason
		} else {
			app.DecisionReason = reason
		}
	}
	if wasApproved {
		app.Revoked = true
		app.RevokedAt = &now
	}
	app.Revision++
	if am.dir != "" {
		if err := am.persist(app); err != nil {
			*app = previous
			return nil, false, err
		}
	}
	copy := *app
	return &copy, true, nil
}

// CloseApproval atomically closes and revokes a REQUESTED or unconsumed
// APPROVED approval when its owning run closes. If close wins after allow but
// before consume, the unconsumed grant is invalidated under the same fence.
func (am *ApprovalManager) CloseApproval(approvalID, reason string, now time.Time) (*RuntimeApproval, bool, error) {
	am.mu.Lock()
	defer am.mu.Unlock()
	unlock, err := am.lockStorage()
	if err != nil {
		return nil, false, err
	}
	defer unlock()

	if err := am.refresh(approvalID); err != nil {
		return nil, false, err
	}
	app, exists := am.approvals[approvalID]
	if !exists {
		return nil, false, fmt.Errorf("approval %s not found", approvalID)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if app.Status != ApprovalRequested && app.Status != ApprovalApproved {
		copy := *app
		return &copy, false, nil
	}
	previous := *app
	app.Status = ApprovalClosed
	app.Revoked = true
	app.RevokedAt = &now
	app.RevokedReason = reason
	if app.ResolvedAt == nil {
		app.ResolvedAt = &now
	}
	app.Revision++
	if am.dir != "" {
		if err := am.persist(app); err != nil {
			*app = previous
			return nil, false, err
		}
	}
	copy := *app
	return &copy, true, nil
}

// ReconcileInterrupted evaluates an interrupted REQUESTED approval. When its
// binding is intact and its deadline has not elapsed, it remains REQUESTED for
// replay; otherwise it is closed and revoked as INTERRUPTED.
func (am *ApprovalManager) ReconcileInterrupted(approvalID string, bindingIntact bool, reason string, now time.Time) (*RuntimeApproval, bool, error) {
	am.mu.Lock()
	defer am.mu.Unlock()
	unlock, err := am.lockStorage()
	if err != nil {
		return nil, false, err
	}
	defer unlock()

	if err := am.refresh(approvalID); err != nil {
		return nil, false, err
	}
	app, exists := am.approvals[approvalID]
	if !exists {
		return nil, false, fmt.Errorf("approval %s not found", approvalID)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if app.Status != ApprovalRequested {
		copy := *app
		return &copy, false, nil
	}
	if bindingIntact && app.ExpiresAt != nil && now.Before(*app.ExpiresAt) {
		copy := *app
		return &copy, false, nil
	}
	previous := *app
	app.Status = ApprovalInterrupted
	app.Revoked = true
	app.RevokedAt = &now
	app.RevokedReason = reason
	app.ResolvedAt = &now
	app.Revision++
	if am.dir != "" {
		if err := am.persist(app); err != nil {
			*app = previous
			return nil, false, err
		}
	}
	copy := *app
	return &copy, true, nil
}

// ValidateAndConsume validates the approval against current action digest and consumes it (one-shot).
func (am *ApprovalManager) ValidateAndConsume(approvalID, currentActionDigest, currentStateDigest string, now time.Time) error {
	am.mu.Lock()
	defer am.mu.Unlock()
	unlock, err := am.lockStorage()
	if err != nil {
		return err
	}
	defer unlock()

	if err := am.refresh(approvalID); err != nil {
		return err
	}
	app, exists := am.approvals[approvalID]

	if !exists {
		return fmt.Errorf("approval %s not found", approvalID)
	}

	previous := *app
	if now.IsZero() {
		now = time.Now().UTC()
	}

	// Never overwrite an already-terminal approval state with EXPIRED.
	if app.Status != ApprovalApproved {
		switch app.Status {
		case ApprovalDenied:
			return ErrApprovalDenied
		case ApprovalExpired:
			return fmt.Errorf("%w: approval expired at %s", ErrApprovalTOCTOUViolation, app.ExpiresAt)
		case ApprovalRequested:
			if app.ExpiresAt != nil && !now.Before(*app.ExpiresAt) {
				app.Status = ApprovalExpired
				app.ResolvedAt = &now
				app.Revision++
				if am.dir != "" {
					if err := am.persist(app); err != nil {
						*app = previous
						return err
					}
				}
				return fmt.Errorf("%w: approval expired at %s", ErrApprovalTOCTOUViolation, app.ExpiresAt)
			}
			return fmt.Errorf("%w: approval status is %s, required APPROVED", ErrApprovalRequired, app.Status)
		case ApprovalSuperseded:
			return fmt.Errorf("%w: approval superseded by %s", ErrApprovalTOCTOUViolation, app.ReplacementID)
		case ApprovalClosed, ApprovalInterrupted, ApprovalInvalidated:
			return fmt.Errorf("%w: approval status is %s (revoked)", ErrApprovalTOCTOUViolation, app.Status)
		default:
			return fmt.Errorf("%w: approval status is %s, required APPROVED", ErrApprovalRequired, app.Status)
		}
	}

	if app.Revoked {
		return fmt.Errorf("%w: approval grant has been revoked", ErrApprovalTOCTOUViolation)
	}

	if app.ExpiresAt != nil && !now.Before(*app.ExpiresAt) {
		app.Status = ApprovalExpired
		app.Revision++
		if am.dir != "" {
			if err := am.persist(app); err != nil {
				*app = previous
				return err
			}
		}
		return fmt.Errorf("%w: approval expired at %s", ErrApprovalTOCTOUViolation, app.ExpiresAt)
	}

	// Exact action check: prevents TOCTOU mutation of approved action
	if app.ActionDigest != currentActionDigest {
		app.Status = ApprovalInvalidated
		app.Revision++
		if am.dir != "" {
			if err := am.persist(app); err != nil {
				*app = previous
				return err
			}
		}
		return fmt.Errorf("%w: action digest mismatch (approved %s, current %s)",
			ErrApprovalTOCTOUViolation, app.ActionDigest, currentActionDigest)
	}

	// State check if provided
	if currentStateDigest != "" && app.StateDigest != "" && app.StateDigest != currentStateDigest {
		app.Status = ApprovalInvalidated
		app.Revision++
		if am.dir != "" {
			if err := am.persist(app); err != nil {
				*app = previous
				return err
			}
		}
		return fmt.Errorf("%w: state digest mismatch since approval was given", ErrApprovalTOCTOUViolation)
	}

	// One-shot consumption is successful but terminal. Keep it distinct from
	// INVALIDATED so a provider bridge can resume the exact live turn while a
	// stale/tampered request can never be mistaken for an accepted decision.
	app.Status = ApprovalConsumed
	app.Revision++
	if am.dir != "" {
		if err := am.persist(app); err != nil {
			*app = previous
			return err
		}
	}
	return nil
}

// GetApproval retrieves an approval by ID.
func (am *ApprovalManager) GetApproval(approvalID string) (*RuntimeApproval, error) {
	am.mu.Lock()
	defer am.mu.Unlock()
	unlock, err := am.lockStorage()
	if err != nil {
		return nil, err
	}
	defer unlock()

	if err := am.refresh(approvalID); err != nil {
		return nil, err
	}
	app, exists := am.approvals[approvalID]

	if !exists {
		return nil, fmt.Errorf("approval %s not found", approvalID)
	}
	copy := *app
	return &copy, nil
}

// ListApprovals returns copies of every durable approval known to this
// manager.  It is the canonical approval queue: consumers must not infer the
// queue solely from a run's cached approval map, because native provider
// approvals may be raised while a task is already running.
func (am *ApprovalManager) ListApprovals() ([]RuntimeApproval, error) {
	am.mu.Lock()
	defer am.mu.Unlock()
	unlock, err := am.lockStorage()
	if err != nil {
		return nil, err
	}
	defer unlock()

	if am.dir != "" {
		entries, err := os.ReadDir(am.dir)
		if err != nil {
			return nil, fmt.Errorf("list durable approvals: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(am.dir, entry.Name()))
			if err != nil {
				return nil, fmt.Errorf("read durable approval %s: %w", entry.Name(), err)
			}
			var loaded RuntimeApproval
			if err := json.Unmarshal(data, &loaded); err != nil {
				return nil, fmt.Errorf("decode durable approval %s: %w", entry.Name(), err)
			}
			if loaded.ApprovalID != "" {
				am.approvals[loaded.ApprovalID] = &loaded
			}
		}
	}
	result := make([]RuntimeApproval, 0, len(am.approvals))
	for _, approval := range am.approvals {
		if approval != nil {
			result = append(result, *approval)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ApprovalID < result[j].ApprovalID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

func (am *ApprovalManager) persist(app *RuntimeApproval) error {
	raw, err := json.MarshalIndent(app, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(am.dir, ".approval-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err = temporary.Write(raw); err != nil {
		temporary.Close()
		return err
	}
	if err = temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(am.dir, app.ApprovalID+".json")); err != nil {
		return err
	}
	directory, err := os.Open(am.dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func (am *ApprovalManager) ApproveContext(ctx context.Context, id, actor, reason string, now time.Time) error {
	_, err := am.DecideContext(ctx, id, true, actor, reason, now)
	return err
}
func (am *ApprovalManager) DenyContext(ctx context.Context, id, actor, reason string, now time.Time) error {
	_, err := am.DecideContext(ctx, id, false, actor, reason, now)
	return err
}

// Serialize file decisions across Runtime instances, then refresh under the
// lock so a second process cannot decide or consume a cached pending record.
func (am *ApprovalManager) lockStorage() (func(), error) {
	if am.dir == "" {
		return func() {}, nil
	}
	file, err := os.OpenFile(filepath.Join(am.dir, ".approval.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); _ = file.Close() }, nil
}
func (am *ApprovalManager) refresh(id string) error {
	if am.dir == "" {
		return nil
	}
	if id == "" || filepath.Base(id) != id {
		return ErrRunInvalid
	}
	raw, err := os.ReadFile(filepath.Join(am.dir, id+".json"))
	if os.IsNotExist(err) {
		delete(am.approvals, id)
		return nil
	}
	if err != nil {
		return err
	}
	var loaded RuntimeApproval
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return err
	}
	if loaded.ApprovalID != id {
		return ErrRunInvalid
	}
	am.approvals[id] = &loaded
	return nil
}
