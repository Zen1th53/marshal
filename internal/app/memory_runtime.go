package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

type MemoryServiceStatus struct {
	Version   string `json:"version"`
	Healthy   bool   `json:"healthy"`
	ProjectID string `json:"project_id"`
}

type RememberRequest struct {
	ProjectID   string           `json:"project_id"`
	Title       string           `json:"title"`
	Body        string           `json:"body"`
	ScopeID     string           `json:"scope_id"`
	Kind        model.MemoryKind `json:"kind"`
	EvidenceIDs []string         `json:"evidence_ids,omitempty"`
}

type PromoteRequest struct {
	MemoryID string `json:"memory_id"`
	ScopeID  string `json:"scope_id"`
}

type RecallRequest struct {
	ProjectID       string   `json:"project_id"`
	Query           string   `json:"query"`
	AllowedScopeIDs []string `json:"allowed_scope_ids"`
}

type RecallItem struct {
	ID        string                `json:"id"`
	Title     string                `json:"title"`
	Kind      model.MemoryKind      `json:"kind"`
	Lifecycle model.MemoryLifecycle `json:"lifecycle"`
}

type RecallResponse struct {
	Results []RecallItem `json:"results"`
}

type MemoryService struct {
	mu         sync.RWMutex
	authorizer *authz.MemoryAuthorizer
	records    map[string]model.MemoryRecordV2
}

func NewMemoryService() *MemoryService {
	return &MemoryService{
		authorizer: authz.NewMemoryAuthorizer(),
		records:    make(map[string]model.MemoryRecordV2),
	}
}

func (s *MemoryService) Version() string {
	return "2.0.0"
}

func (s *MemoryService) Status(ctx context.Context, projectID string) (MemoryServiceStatus, error) {
	if err := ctx.Err(); err != nil {
		return MemoryServiceStatus{}, err
	}
	return MemoryServiceStatus{
		Version:   s.Version(),
		Healthy:   true,
		ProjectID: projectID,
	}, nil
}

func (s *MemoryService) Remember(ctx context.Context, principal authz.Principal, req RememberRequest) (model.MemoryRecordV2, error) {
	if err := ctx.Err(); err != nil {
		return model.MemoryRecordV2{}, err
	}

	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryRemember, req.ScopeID, model.MemoryCandidate); err != nil {
		return model.MemoryRecordV2{}, err
	}

	now := time.Now().UTC()
	h := sha256.New()
	fmt.Fprintf(h, "%s:%s:%s", req.ProjectID, req.Title, req.Body)
	idHash := hex.EncodeToString(h.Sum(nil))[:16]

	rec := model.MemoryRecordV2{
		ID:          fmt.Sprintf("MEM-SVC-%s", idHash),
		ProjectID:   req.ProjectID,
		Kind:        req.Kind,
		Lifecycle:   model.MemoryCandidate,
		Confidence:  model.ConfidenceInferred,
		Authority:   model.AuthorityAgent,
		Title:       req.Title,
		Body:        req.Body,
		Scope:       string(model.ScopeProject),
		ScopeID:     req.ScopeID,
		EvidenceIDs: req.EvidenceIDs,
		ObservedAt:  now,
		IngestedAt:  now,
		ValidFrom:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		Source: model.MemorySource{
			Kind:      "runtime_service",
			Reference: principal.ID,
		},
	}
	rec.ContentDigest = rec.CanonicalDigest()

	s.mu.Lock()
	s.records[rec.ID] = rec
	s.mu.Unlock()

	return rec, nil
}

func (s *MemoryService) Promote(ctx context.Context, principal authz.Principal, req PromoteRequest) (model.MemoryRecordV2, error) {
	if err := ctx.Err(); err != nil {
		return model.MemoryRecordV2{}, err
	}

	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryPromote, req.ScopeID, model.MemoryDurable); err != nil {
		return model.MemoryRecordV2{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[req.MemoryID]
	if !ok {
		return model.MemoryRecordV2{}, fmt.Errorf("record %s not found", req.MemoryID)
	}

	rec.Lifecycle = model.MemoryDurable
	rec.Authority = model.AuthorityOperator
	rec.UpdatedAt = time.Now().UTC()
	rec.ContentDigest = rec.CanonicalDigest()
	s.records[req.MemoryID] = rec

	return rec, nil
}

func (s *MemoryService) Recall(ctx context.Context, principal authz.Principal, req RecallRequest) (RecallResponse, error) {
	if err := ctx.Err(); err != nil {
		return RecallResponse{}, err
	}

	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryRecall, req.ProjectID, model.MemoryDurable); err != nil {
		return RecallResponse{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	allowedScopeMap := make(map[string]bool)
	for _, sc := range req.AllowedScopeIDs {
		allowedScopeMap[sc] = true
	}

	var results []RecallItem
	for _, rec := range s.records {
		if rec.ProjectID != req.ProjectID {
			continue
		}
		if len(req.AllowedScopeIDs) > 0 && rec.ScopeID != "" && !allowedScopeMap[rec.ScopeID] {
			continue
		}
		results = append(results, RecallItem{
			ID:        rec.ID,
			Title:     rec.Title,
			Kind:      rec.Kind,
			Lifecycle: rec.Lifecycle,
		})
	}

	return RecallResponse{Results: results}, nil
}
