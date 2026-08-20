package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
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
	ProjectID string `json:"project_id"`
	MemoryID  string `json:"memory_id"`
	ScopeID   string `json:"scope_id"`
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

// MemoryService is the canonical product-facing memory facade. It is backed
// exclusively by the persistent store (memory_records_v2) — there is no
// separate in-memory source of truth.
type MemoryService struct {
	store      *store.Store
	authorizer *authz.MemoryAuthorizer
}

func NewMemoryService(st *store.Store) *MemoryService {
	return &MemoryService{
		store:      st,
		authorizer: authz.NewMemoryAuthorizer(),
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
	if s == nil || s.store == nil {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: memory store is unavailable", model.ErrUnavailable)
	}
	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryRemember, req.ScopeID, model.MemoryCandidate); err != nil {
		return model.MemoryRecordV2{}, err
	}

	kind := req.Kind
	if !kind.IsValid() {
		kind = model.MemoryKindSemantic
	}
	now := time.Now().UTC()
	id, err := model.NewID("MEM-")
	if err != nil {
		return model.MemoryRecordV2{}, err
	}

	rec := model.MemoryRecordV2{
		ID:          id,
		ProjectID:   req.ProjectID,
		Kind:        kind,
		Lifecycle:   model.MemoryCandidate,
		Confidence:  model.ConfidenceInferred,
		Authority:   model.AuthorityAgent,
		Title:       req.Title,
		Body:        req.Body,
		Scope:       string(model.ScopeProject),
		ScopeID:     req.ProjectID,
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
	if err := s.store.WriteMemoryV2(ctx, rec); err != nil {
		return model.MemoryRecordV2{}, err
	}
	return rec, nil
}

func (s *MemoryService) Promote(ctx context.Context, principal authz.Principal, req PromoteRequest) (model.MemoryRecordV2, error) {
	if err := ctx.Err(); err != nil {
		return model.MemoryRecordV2{}, err
	}
	if s == nil || s.store == nil {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: memory store is unavailable", model.ErrUnavailable)
	}
	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryPromote, req.ScopeID, model.MemoryDurable); err != nil {
		return model.MemoryRecordV2{}, err
	}

	existing, err := s.store.GetMemoryV2(ctx, req.ProjectID, req.MemoryID)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	promoted, err := s.store.UpdateMemory(ctx, existing.ProjectID, req.MemoryID, existing.Revision, func(rec *model.MemoryRecordV2) error {
		rec.Lifecycle = model.MemoryDurable
		rec.Authority = model.AuthorityOperator
		rec.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return promoted, nil
}

func (s *MemoryService) Recall(ctx context.Context, principal authz.Principal, req RecallRequest) (RecallResponse, error) {
	if err := ctx.Err(); err != nil {
		return RecallResponse{}, err
	}
	if s == nil || s.store == nil {
		return RecallResponse{}, fmt.Errorf("%w: memory store is unavailable", model.ErrUnavailable)
	}
	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryRecall, req.ProjectID, model.MemoryDurable); err != nil {
		return RecallResponse{}, err
	}

	records, err := s.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: req.ProjectID})
	if err != nil {
		return RecallResponse{}, err
	}

	allowedScopeMap := make(map[string]bool)
	for _, sc := range req.AllowedScopeIDs {
		allowedScopeMap[sc] = true
	}

	query := strings.ToLower(strings.TrimSpace(req.Query))
	var results []RecallItem
	for _, rec := range records {
		if rec.ProjectID != req.ProjectID {
			continue
		}
		if len(req.AllowedScopeIDs) > 0 && rec.ScopeID != "" && !allowedScopeMap[rec.ScopeID] {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(rec.Title), query) && !strings.Contains(strings.ToLower(rec.Body), query) {
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
