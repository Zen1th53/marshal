package review

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrUnauthorizedApprover = errors.New("approver role does not have authority for protected memory")
	ErrDigestMismatch       = errors.New("candidate payload has changed since review request creation")
	ErrAlreadyResolved      = errors.New("review request has already been approved or rejected")
	ErrRequestNotFound      = errors.New("review request not found")
)

type ReviewStatus string

const (
	StatusPending  ReviewStatus = "pending"
	StatusApproved ReviewStatus = "approved"
	StatusRejected ReviewStatus = "rejected"
)

type ReviewRequest struct {
	RequestID       string       `json:"request_id"`
	MemoryID        string       `json:"memory_id"`
	ProjectID       string       `json:"project_id"`
	CandidateDigest string       `json:"candidate_digest"`
	RequestedBy     string       `json:"requested_by"`
	Status          ReviewStatus `json:"status"`
	ApproverID      string       `json:"approver_id,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	ResolvedAt      *time.Time   `json:"resolved_at,omitempty"`
}

type Manager struct {
	mu       sync.Mutex
	requests map[string]*ReviewRequest
}

func NewManager() *Manager {
	return &Manager{
		requests: make(map[string]*ReviewRequest),
	}
}

func generateRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("REV-%s", hex.EncodeToString(b[:]))
}

// CreateReviewRequest initiates an approval workflow for a candidate record.
func (m *Manager) CreateReviewRequest(ctx context.Context, candidate model.MemoryRecordV2, requestedBy string) (ReviewRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	req := ReviewRequest{
		RequestID:       generateRequestID(),
		MemoryID:        candidate.ID,
		ProjectID:       candidate.ProjectID,
		CandidateDigest: candidate.CanonicalDigest(),
		RequestedBy:     requestedBy,
		Status:          StatusPending,
		CreatedAt:       time.Now().UTC(),
	}

	m.requests[req.RequestID] = &req
	return req, nil
}

// Approve evaluates approver authority, checks digest immutability, and promotes candidate to Durable.
func (m *Manager) Approve(ctx context.Context, requestID string, candidate model.MemoryRecordV2, approverID, approverRole string) (model.MemoryRecordV2, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.requests[requestID]
	if !ok {
		return model.MemoryRecordV2{}, ErrRequestNotFound
	}

	if req.Status != StatusPending {
		return model.MemoryRecordV2{}, ErrAlreadyResolved
	}

	// 1. Role check: only operator, admin, or security_lead can approve protected memory
	if approverRole != "operator" && approverRole != "admin" && approverRole != "security_lead" {
		return model.MemoryRecordV2{}, ErrUnauthorizedApprover
	}

	// 2. Digest check: ensure payload was not tampered/mutated since review creation
	currentDigest := candidate.CanonicalDigest()
	if currentDigest != req.CandidateDigest {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: expected %s, found %s", ErrDigestMismatch, req.CandidateDigest, currentDigest)
	}

	// 3. Mark request approved
	now := time.Now().UTC()
	req.Status = StatusApproved
	req.ApproverID = approverID
	req.ResolvedAt = &now

	// 4. Promote record to Durable with Operator authority
	approvedRec := candidate
	approvedRec.Lifecycle = model.MemoryDurable
	approvedRec.Authority = model.AuthorityOperator
	approvedRec.UpdatedAt = now
	approvedRec.ContentDigest = approvedRec.CanonicalDigest()

	return approvedRec, nil
}
