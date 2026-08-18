package decision

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Engine struct {
	mu      sync.RWMutex
	records map[string]*DecisionRecord
}

func NewEngine() *Engine {
	return &Engine{
		records: make(map[string]*DecisionRecord),
	}
}

func (e *Engine) Propose(ctx context.Context, id, taskID, agentID, title, contextStr, decisionStr string) (*DecisionRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if id == "" || taskID == "" || title == "" {
		return nil, ErrInvalidStatus
	}

	now := time.Now().UTC()
	rec := &DecisionRecord{
		ID:        id,
		TaskID:    taskID,
		AgentID:   agentID,
		Title:     title,
		Context:   contextStr,
		Decision:  decisionStr,
		Status:    StatusProposed,
		CreatedAt: now,
		UpdatedAt: now,
	}
	e.records[id] = rec
	return rec, nil
}

func (e *Engine) Accept(ctx context.Context, id, authorityID string) (*DecisionRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	rec, exists := e.records[id]
	if !exists {
		return nil, fmt.Errorf("decision not found")
	}
	if authorityID == "" {
		return nil, ErrAuthorityRequired
	}
	if rec.Status == StatusAccepted || rec.Status == StatusRejected {
		return nil, ErrAlreadyFinal
	}

	rec.Status = StatusAccepted
	rec.UpdatedAt = time.Now().UTC()
	return rec, nil
}

func (e *Engine) Reject(ctx context.Context, id, authorityID string) (*DecisionRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	rec, exists := e.records[id]
	if !exists {
		return nil, fmt.Errorf("decision not found")
	}
	if authorityID == "" {
		return nil, ErrAuthorityRequired
	}
	if rec.Status == StatusAccepted || rec.Status == StatusRejected {
		return nil, ErrAlreadyFinal
	}

	rec.Status = StatusRejected
	rec.UpdatedAt = time.Now().UTC()
	return rec, nil
}

func (e *Engine) Supersede(ctx context.Context, oldID, newID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	oldRec, existsOld := e.records[oldID]
	if !existsOld {
		return fmt.Errorf("old decision not found")
	}
	if _, existsNew := e.records[newID]; !existsNew {
		return ErrSupersessionInvalid
	}

	oldRec.Status = StatusSuperseded
	oldRec.SupersededBy = newID
	oldRec.UpdatedAt = time.Now().UTC()
	return nil
}
