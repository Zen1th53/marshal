package execution

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AcquireLeaseRequest defines requirements for acquiring a mutation lease.
type AcquireLeaseRequest struct {
	RunID           string
	TaskID          string
	AgentID         string
	Role            string
	ScopedResources []string
	MutationScope   string
	WorktreePath    string
	TTL             time.Duration
	Now             time.Time
}

// LeaseManager manages canonical ownership and resource conflict detection.
type LeaseManager struct {
	mu     sync.RWMutex
	leases map[string]*Lease // leaseID -> Lease
}

// NewLeaseManager creates an in-memory or coordinated lease manager.
func NewLeaseManager() *LeaseManager {
	return &LeaseManager{
		leases: make(map[string]*Lease),
	}
}

// CleanResourcePath normalizes resource paths and blocks path traversal attempts.
func CleanResourcePath(p string) (string, error) {
	cleaned := filepath.Clean(filepath.ToSlash(strings.TrimSpace(p)))
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", fmt.Errorf("%w: path traversal detected in resource %q", ErrIsolationCompromised, p)
	}
	if filepath.IsAbs(cleaned) && !strings.HasPrefix(cleaned, "/project") && !strings.HasPrefix(cleaned, "/workspace") {
		// Enforce relative or workspace-confined paths
		cleaned = strings.TrimPrefix(cleaned, "/")
	}
	return cleaned, nil
}

// ResourcesOverlap checks if two sets of resource paths overlap.
// e.g. "internal/auth" overlaps with "internal/auth/session.go".
func ResourcesOverlap(a, b []string) bool {
	for _, resA := range a {
		cleanA := filepath.Clean(filepath.ToSlash(resA))
		for _, resB := range b {
			cleanB := filepath.Clean(filepath.ToSlash(resB))
			if cleanA == cleanB {
				return true
			}
			// Directory prefix checks
			if strings.HasPrefix(cleanA, cleanB+"/") || strings.HasPrefix(cleanB, cleanA+"/") {
				return true
			}
		}
	}
	return false
}

// AcquireLease attempts to acquire an exclusive mutation lease.
func (lm *LeaseManager) AcquireLease(req AcquireLeaseRequest) (*Lease, error) {
	if req.TaskID == "" || req.AgentID == "" || req.RunID == "" {
		return nil, fmt.Errorf("%w: missing required lease parameters", ErrRunInvalid)
	}

	cleanedResources := make([]string, 0, len(req.ScopedResources))
	for _, r := range req.ScopedResources {
		c, err := CleanResourcePath(r)
		if err != nil {
			return nil, err
		}
		cleanedResources = append(cleanedResources, c)
	}

	ttl := req.TTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}

	lm.mu.Lock()
	defer lm.mu.Unlock()

	// 1. Check for active lease on same task or conflicting resources
	for _, existing := range lm.leases {
		if existing.Status != LeaseActive {
			continue
		}
		// Is existing expired?
		if req.Now.After(existing.ExpiresAt) {
			existing.Status = LeaseExpired
			continue
		}

		// Same task check
		if existing.TaskID == req.TaskID {
			return nil, fmt.Errorf("%w: task %s already has active lease held by %s (lease %s)",
				ErrLeaseConflict, req.TaskID, existing.AgentID, existing.LeaseID)
		}

		// Resource overlap conflict check (unless both use dedicated isolated worktrees)
		if len(cleanedResources) > 0 && len(existing.ScopedResources) > 0 {
			if ResourcesOverlap(cleanedResources, existing.ScopedResources) {
				// If both have different dedicated non-empty worktrees, it's isolated. Otherwise conflict!
				if req.WorktreePath == "" || existing.WorktreePath == "" || req.WorktreePath == existing.WorktreePath {
					return nil, fmt.Errorf("%w: resource conflict between task %s and task %s on resources %v",
						ErrLeaseConflict, req.TaskID, existing.TaskID, cleanedResources)
				}
			}
		}
	}

	leaseID := fmt.Sprintf("lease-%s-%s-%d", req.TaskID, req.AgentID, req.Now.UnixNano())
	l := &Lease{
		LeaseID:         leaseID,
		RunID:           req.RunID,
		TaskID:          req.TaskID,
		AgentID:         req.AgentID,
		Role:            req.Role,
		Status:          LeaseActive,
		AcquiredAt:      req.Now,
		ExpiresAt:       req.Now.Add(ttl),
		HeartbeatAt:     req.Now,
		ScopedResources: cleanedResources,
		MutationScope:   req.MutationScope,
		WorktreePath:    req.WorktreePath,
	}

	lm.leases[leaseID] = l
	return l, nil
}

// RenewLease extends the expiration of an active lease by the authorized owner.
func (lm *LeaseManager) RenewLease(leaseID, agentID string, ttl time.Duration, now time.Time) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	l, exists := lm.leases[leaseID]
	if !exists {
		return fmt.Errorf("%w: lease %s does not exist", ErrRunNotFound, leaseID)
	}

	if l.AgentID != agentID {
		return fmt.Errorf("%w: lease renewal rejected: agent %s is not the owner (%s)",
			ErrUnauthorizedWorker, agentID, l.AgentID)
	}

	if l.Status != LeaseActive {
		return fmt.Errorf("%w: lease %s is %s", ErrLeaseExpired, leaseID, l.Status)
	}

	if now.After(l.ExpiresAt) {
		l.Status = LeaseExpired
		return fmt.Errorf("%w: lease %s expired at %v", ErrLeaseExpired, leaseID, l.ExpiresAt)
	}

	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	l.ExpiresAt = now.Add(ttl)
	l.HeartbeatAt = now
	return nil
}

// TakeoverLease transfers ownership of a stale or failed lease to a new agent.
func (lm *LeaseManager) TakeoverLease(taskID, newAgentID, role, reason string, ttl time.Duration, now time.Time) (*Lease, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	var previous *Lease
	for _, l := range lm.leases {
		if l.TaskID == taskID && (l.Status == LeaseActive || l.Status == LeaseExpired) {
			previous = l
			break
		}
	}

	if previous == nil {
		return nil, fmt.Errorf("%w: no lease found for task %s to takeover", ErrRunNotFound, taskID)
	}

	// Can only takeover if previous is expired or explicitly abandoned
	if previous.Status == LeaseActive && now.Before(previous.ExpiresAt) {
		return nil, fmt.Errorf("%w: cannot takeover active unexpired lease %s (held by %s until %v)",
			ErrLeaseConflict, previous.LeaseID, previous.AgentID, previous.ExpiresAt)
	}

	previous.Status = LeaseRevoked

	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	newLeaseID := fmt.Sprintf("lease-%s-%s-%d", taskID, newAgentID, now.UnixNano())
	newLease := &Lease{
		LeaseID:            newLeaseID,
		RunID:              previous.RunID,
		TaskID:             taskID,
		AgentID:            newAgentID,
		Role:               role,
		Status:             LeaseActive,
		AcquiredAt:         now,
		ExpiresAt:          now.Add(ttl),
		HeartbeatAt:        now,
		ScopedResources:    append([]string(nil), previous.ScopedResources...),
		MutationScope:      previous.MutationScope,
		WorktreePath:       previous.WorktreePath,
		TakeoverProvenance: fmt.Sprintf("Takeover from %s (lease %s) at %v: %s", previous.AgentID, previous.LeaseID, now, reason),
	}

	lm.leases[newLeaseID] = newLease
	return newLease, nil
}

// ReleaseLease voluntarily releases an active lease.
func (lm *LeaseManager) ReleaseLease(leaseID, agentID string, now time.Time) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	l, exists := lm.leases[leaseID]
	if !exists {
		return fmt.Errorf("%w: lease %s not found", ErrRunNotFound, leaseID)
	}

	if l.AgentID != agentID {
		return fmt.Errorf("%w: release refused: caller %s is not lease owner %s",
			ErrUnauthorizedWorker, agentID, l.AgentID)
	}

	l.Status = LeaseReleased
	return nil
}

// VerifyOwner confirms that a given lease is active and owned by the agent.
func (lm *LeaseManager) VerifyOwner(taskID, agentID, leaseID string, now time.Time) error {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	l, exists := lm.leases[leaseID]
	if !exists {
		return fmt.Errorf("%w: lease %s does not exist", ErrRunNotFound, leaseID)
	}

	if l.TaskID != taskID {
		return fmt.Errorf("%w: lease %s belongs to task %s, not %s", ErrUnauthorizedWorker, leaseID, l.TaskID, taskID)
	}

	if l.AgentID != agentID {
		return fmt.Errorf("%w: lease %s owned by %s, not %s", ErrUnauthorizedWorker, leaseID, l.AgentID, agentID)
	}

	if l.Status != LeaseActive {
		return fmt.Errorf("%w: lease %s is %s", ErrLeaseExpired, leaseID, l.Status)
	}

	if now.After(l.ExpiresAt) {
		return fmt.Errorf("%w: lease %s expired at %v", ErrLeaseExpired, leaseID, l.ExpiresAt)
	}

	return nil
}
