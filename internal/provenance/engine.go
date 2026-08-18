package provenance

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Engine struct {
	mu      sync.RWMutex
	records map[string]*ChangeRecord
}

func NewEngine() *Engine {
	return &Engine{
		records: make(map[string]*ChangeRecord),
	}
}

func (e *Engine) Begin(ctx context.Context, changeID, taskID, agentID, provider, contextDigest, patchDigest string) (*ChangeRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if changeID == "" || taskID == "" || agentID == "" {
		return nil, fmt.Errorf("%w: missing required identity fields", ErrChangeNotFound)
	}

	if rec, exists := e.records[changeID]; exists {
		if rec.Sealed {
			return nil, ErrAlreadySealed
		}
		return rec, nil
	}

	rec := &ChangeRecord{
		ChangeID:      changeID,
		TaskID:        taskID,
		AgentID:       agentID,
		Provider:      provider,
		ContextDigest: contextDigest,
		PatchDigest:   patchDigest,
		ToolCallIDs:   []string{},
		EvidenceIDs:   []string{},
		ApprovalIDs:   []string{},
		Sealed:        false,
		CreatedAt:     time.Now().UTC(),
	}
	e.records[changeID] = rec
	return rec, nil
}

func (e *Engine) AttachToolCall(ctx context.Context, changeID, toolCallID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	rec, exists := e.records[changeID]
	if !exists {
		return ErrChangeNotFound
	}
	if rec.Sealed {
		return ErrAlreadySealed
	}
	rec.ToolCallIDs = append(rec.ToolCallIDs, toolCallID)
	return nil
}

func (e *Engine) AttachEvidence(ctx context.Context, changeID, evidenceID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	rec, exists := e.records[changeID]
	if !exists {
		return ErrChangeNotFound
	}
	if rec.Sealed {
		return ErrAlreadySealed
	}
	rec.EvidenceIDs = append(rec.EvidenceIDs, evidenceID)
	return nil
}

func (e *Engine) AttachApproval(ctx context.Context, changeID, approvalID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	rec, exists := e.records[changeID]
	if !exists {
		return ErrChangeNotFound
	}
	if rec.Sealed {
		return ErrAlreadySealed
	}
	rec.ApprovalIDs = append(rec.ApprovalIDs, approvalID)
	return nil
}

func (e *Engine) AttachPatch(ctx context.Context, changeID, patchDigest string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	rec, exists := e.records[changeID]
	if !exists {
		return ErrChangeNotFound
	}
	if rec.Sealed {
		return ErrAlreadySealed
	}
	if rec.PatchDigest != "" && rec.PatchDigest != patchDigest {
		return ErrPatchMismatch
	}
	rec.PatchDigest = patchDigest
	return nil
}

func (e *Engine) Seal(ctx context.Context, changeID, commitSHA string) (*ChangeRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	rec, exists := e.records[changeID]
	if !exists {
		return nil, ErrChangeNotFound
	}
	if rec.Sealed {
		return nil, ErrAlreadySealed
	}
	if commitSHA != "" && !ValidateSHA(commitSHA) {
		return nil, ErrInvalidCommit
	}
	rec.CommitSHA = commitSHA
	rec.Sealed = true
	rec.SealedAt = time.Now().UTC()
	return rec, nil
}

func (e *Engine) Trace(ctx context.Context, changeID string) (*ChainCustodyView, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	rec, exists := e.records[changeID]
	if !exists {
		return nil, ErrChangeNotFound
	}
	return &ChainCustodyView{
		Record:    *rec,
		ChainHash: rec.ComputeChainHash(),
	}, nil
}
