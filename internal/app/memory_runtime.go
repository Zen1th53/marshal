package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/memory/conflict"
	"github.com/Zen1th53/marshal/internal/memory/extract"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/memory/index/graph"
	"github.com/Zen1th53/marshal/internal/memory/index/lexical"
	"github.com/Zen1th53/marshal/internal/memory/index/vector"
	"github.com/Zen1th53/marshal/internal/memory/retrieval/cache"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

type MemoryServiceStatus struct {
	Version   string `json:"version"`
	Healthy   bool   `json:"healthy"`
	ProjectID string `json:"project_id"`
}

type CandidateResult struct {
	MemoryID string
	Track    string // "exact", "lexical", "vector", "graph"
	Score    float64
	Degraded bool
}

type CandidateProvider interface {
	Name() string
	QueryCandidates(ctx context.Context, projectID string, allowedScopeIDs []string, query string, limit int) ([]CandidateResult, error)
}

const (
	maxCandidateProviders  = 8
	lexicalCandidateLimit  = 50
	providerCandidateLimit = 20
	graphTraversalDepth    = 2
	derivedTrackTimeout    = 250 * time.Millisecond
)

type RememberRequest struct {
	ProjectID   string                `json:"project_id"`
	Title       string                `json:"title"`
	Body        string                `json:"body"`
	Scope       model.MemoryScopeKind `json:"scope,omitempty"`
	ScopeID     string                `json:"scope_id"`
	Kind        model.MemoryKind      `json:"kind"`
	EvidenceIDs []string              `json:"evidence_ids,omitempty"`
}

type PromoteRequest struct {
	ProjectID string `json:"project_id"`
	MemoryID  string `json:"memory_id"`
	ScopeID   string `json:"scope_id"`
	Rationale string `json:"rationale,omitempty"`
}

type OutcomeCaptureRequest struct {
	ProjectID      string
	TaskID         string
	TaskTitle      string
	RunID          string
	SessionID      string
	AgentID        string
	Provider       string
	Status         string
	ExitStatus     int
	BaseCommit     string
	HeadCommit     string
	Branch         string
	EvidenceIDs    []string
	FilesChanged   []string
	TestsRun       []string
	ErrorSignature string
	FailureReason  string
	RetryCondition string
	Environment    map[string]string
}

type RecallRequest struct {
	ProjectID         string            `json:"project_id"`
	Query             string            `json:"query"`
	AllowedScopeIDs   []string          `json:"allowed_scope_ids"`
	CurrentHead       string            `json:"current_head,omitempty"`
	CurrentBranch     string            `json:"current_branch,omitempty"`
	ModifiedFiles     []string          `json:"modified_files,omitempty"`
	DeletedFiles      []string          `json:"deleted_files,omitempty"`
	RenamedFiles      map[string]string `json:"renamed_files,omitempty"`
	CurrentFileHashes map[string]string `json:"current_file_hashes,omitempty"`
	ExistingSymbols   map[string]bool   `json:"existing_symbols,omitempty"`
	InvalidatedTests  []string          `json:"invalidated_tests,omitempty"`
	MaxRecords        int               `json:"max_records,omitempty"`
	MaxBytes          int               `json:"max_bytes,omitempty"`
	RunID             string            `json:"run_id,omitempty"`
	TaskID            string            `json:"task_id,omitempty"`
	Provider          string            `json:"provider,omitempty"`
}

type RecallItem struct {
	ID        string                `json:"id"`
	Title     string                `json:"title"`
	Kind      model.MemoryKind      `json:"kind"`
	Lifecycle model.MemoryLifecycle `json:"lifecycle"`
}

type RecallResponse struct {
	Results []RecallItem     `json:"results"`
	Receipt RetrievalReceipt `json:"receipt"`
	Context string           `json:"context,omitempty"`
}

type RetrievalDecision struct {
	MemoryID      string   `json:"memory_id"`
	Included      bool     `json:"included"`
	Reason        string   `json:"reason"`
	MatchedTracks []string `json:"matched_tracks,omitempty"`
	Authority     string   `json:"authority,omitempty"`
	Lifecycle     string   `json:"lifecycle,omitempty"`
	Stale         bool     `json:"stale"`
	Bytes         int      `json:"bytes"`
}

type RetrievalReceipt struct {
	ReceiptID string `json:"receipt_id"`
	// Query is returned only to the caller in the immediate response. Raw
	// prompts are never persisted because they may contain credentials.
	Query           string              `json:"query,omitempty"`
	QueryDigest     string              `json:"query_digest"`
	ProjectID       string              `json:"project_id"`
	AllowedScopeIDs []string            `json:"allowed_scope_ids"`
	CurrentHead     string              `json:"current_head,omitempty"`
	CurrentBranch   string              `json:"current_branch,omitempty"`
	MaxRecords      int                 `json:"max_records"`
	MaxBytes        int                 `json:"max_bytes"`
	ConsumedBytes   int                 `json:"consumed_bytes"`
	Decisions       []RetrievalDecision `json:"decisions"`
	DeniedCount     int                 `json:"denied_count,omitempty"`
	RunID           string              `json:"run_id,omitempty"`
	TaskID          string              `json:"task_id,omitempty"`
	Provider        string              `json:"provider,omitempty"`
	EvidenceIDs     []string            `json:"evidence_ids,omitempty"`
	OutcomeMemoryID string              `json:"outcome_memory_id,omitempty"`
	OutcomeStatus   string              `json:"outcome_status,omitempty"`
	GeneratedAt     time.Time           `json:"generated_at"`
}

// MemoryService is the canonical product-facing memory facade. It is backed
// exclusively by the persistent store (memory_records_v2) — there is no
// separate in-memory source of truth. Lexical, vector, and graph indexes
// remain disposable derived projections.
type MemoryService struct {
	store            *store.Store
	authorizer       *authz.MemoryAuthorizer
	lexicalIndex     *lexical.LexicalIndex
	graphIndex       *graph.GraphIndex
	vectorStore      vector.VectorBackend
	cache            *cache.BoundedCache
	conflictDetector *conflict.Detector
	extractPipeline  *extract.Pipeline
	sessionImporter  *importer.SessionImporter
	providers        []CandidateProvider
	mu               sync.RWMutex
}

func NewMemoryService(st *store.Store) *MemoryService {
	svc := &MemoryService{
		store:        st,
		authorizer:   authz.NewMemoryAuthorizer(),
		lexicalIndex: lexical.NewLexicalIndex(),
		graphIndex:   graph.NewGraphIndex(),
		// Semantic retrieval is optional. A vector backend is installed only
		// when the runtime also has a real query/document embedder; empty
		// embeddings must never masquerade as a working semantic channel.
		vectorStore:      nil,
		cache:            cache.NewBoundedCache(cache.Config{MaxEntries: 500, TTL: 5 * time.Minute}),
		conflictDetector: conflict.NewDetector(),
		extractPipeline:  extract.NewPipeline(),
		sessionImporter:  importer.NewSessionImporter(importer.Config{Firewall: security.NewFirewall(security.FirewallConfig{})}),
	}
	return svc
}

func (s *MemoryService) RegisterCandidateProvider(p CandidateProvider) {
	if p == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.providers) >= maxCandidateProviders {
		return
	}
	s.providers = append(s.providers, p)
}

func (s *MemoryService) RebuildProjections(ctx context.Context, projectID string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("%w: memory store is unavailable", model.ErrUnavailable)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: projectID})
	if err != nil {
		return fmt.Errorf("list canonical records for index rebuild: %w", err)
	}

	// 1. Rebuild lexical
	if err := s.lexicalIndex.Rebuild(ctx, records); err != nil {
		return fmt.Errorf("rebuild lexical index: %w", err)
	}

	// 2. Rebuild graph
	s.graphIndex = graph.NewGraphIndex()
	for _, r := range records {
		if r.Lifecycle == model.MemoryTombstoned || r.Lifecycle == model.MemoryRejected {
			continue
		}
		if err := s.graphIndex.AddNode(ctx, graph.GraphNode{
			ID:      r.ID,
			Kind:    string(r.Kind),
			ScopeID: r.ScopeID,
			Labels:  []string{r.Scope, string(r.Authority)},
		}); err != nil {
			return fmt.Errorf("rebuild graph node %s: %w", r.ID, err)
		}
		for _, evID := range r.EvidenceIDs {
			if err := s.graphIndex.AddEdge(ctx, graph.GraphEdge{
				FromID:     r.ID,
				ToID:       evID,
				Relation:   "evidenced_by",
				ValidFrom:  r.ValidFrom,
				Confidence: 1.0,
			}); err != nil {
				return fmt.Errorf("rebuild evidence edge for %s: %w", r.ID, err)
			}
		}
		for _, supID := range r.SupersedesID {
			if err := s.graphIndex.AddEdge(ctx, graph.GraphEdge{
				FromID:     r.ID,
				ToID:       supID,
				Relation:   "supersedes",
				ValidFrom:  r.ValidFrom,
				Confidence: 1.0,
			}); err != nil {
				return fmt.Errorf("rebuild supersession edge for %s: %w", r.ID, err)
			}
		}
	}

	// 3. Rebuild vector only when a configured semantic provider supplies
	// actual embeddings. The default local runtime deliberately has none.
	if s.vectorStore != nil {
		if err := s.vectorStore.Rebuild(ctx, nil); err != nil {
			return fmt.Errorf("clear vector projection: %w", err)
		}
	}

	// 4. Purge cache
	s.cache.Purge()
	return nil
}

func (s *MemoryService) InvalidateRecord(ctx context.Context, projectID, memoryID, scopeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.lexicalIndex.RemoveRecord(ctx, memoryID); err != nil {
		return err
	}
	if err := s.graphIndex.RemoveNode(ctx, memoryID); err != nil {
		return err
	}
	if s.vectorStore != nil {
		if err := s.vectorStore.DeleteVector(ctx, memoryID); err != nil {
			return err
		}
	}
	s.cache.InvalidateScope(scopeID)
	return nil
}

func (s *MemoryService) IndexRecord(ctx context.Context, rec model.MemoryRecordV2) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemoryRejected {
		if err := s.lexicalIndex.RemoveRecord(ctx, rec.ID); err != nil {
			return err
		}
		if err := s.graphIndex.RemoveNode(ctx, rec.ID); err != nil {
			return err
		}
		if s.vectorStore != nil {
			if err := s.vectorStore.DeleteVector(ctx, rec.ID); err != nil {
				return err
			}
		}
	} else {
		if err := s.lexicalIndex.IndexRecord(ctx, rec); err != nil {
			return err
		}
		if err := s.graphIndex.RemoveNode(ctx, rec.ID); err != nil {
			return err
		}
		if err := s.graphIndex.AddNode(ctx, graph.GraphNode{
			ID:      rec.ID,
			Kind:    string(rec.Kind),
			ScopeID: rec.ScopeID,
			Labels:  []string{rec.Scope, string(rec.Authority)},
		}); err != nil {
			return err
		}
		for _, evID := range rec.EvidenceIDs {
			if err := s.graphIndex.AddEdge(ctx, graph.GraphEdge{
				FromID:     rec.ID,
				ToID:       evID,
				Relation:   "evidenced_by",
				ValidFrom:  rec.ValidFrom,
				Confidence: 1.0,
			}); err != nil {
				return err
			}
		}
		for _, supID := range rec.SupersedesID {
			if err := s.graphIndex.AddEdge(ctx, graph.GraphEdge{
				FromID:     rec.ID,
				ToID:       supID,
				Relation:   "supersedes",
				ValidFrom:  rec.ValidFrom,
				Confidence: 1.0,
			}); err != nil {
				return err
			}
		}
	}
	s.cache.InvalidateScope(rec.ScopeID)
	return nil
}

func (s *MemoryService) Version() string {
	return "2.0.0"
}

func (s *MemoryService) Status(ctx context.Context, projectID string) (MemoryServiceStatus, error) {
	if err := ctx.Err(); err != nil {
		return MemoryServiceStatus{}, err
	}
	if s == nil || s.store == nil {
		return MemoryServiceStatus{}, fmt.Errorf("%w: memory store is unavailable", model.ErrUnavailable)
	}
	if _, err := s.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: projectID, Limit: 1}); err != nil {
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
	kind := req.Kind
	if !kind.IsValid() {
		kind = model.MemoryKindSemantic
	}
	now := time.Now().UTC()
	scopeKind := req.Scope
	if scopeKind == "" {
		scopeKind = model.ScopeProject
	}
	scopeID := strings.TrimSpace(req.ScopeID)
	if scopeID == "" && scopeKind == model.ScopeProject {
		scopeID = req.ProjectID
	}
	if _, err := model.NewMemoryScope(string(scopeKind), scopeID); err != nil {
		return model.MemoryRecordV2{}, err
	}
	if scopeKind == model.ScopeProject && scopeID != req.ProjectID {
		return model.MemoryRecordV2{}, authz.ErrUnauthorized
	}
	if (scopeKind == model.ScopeOperatorPrivate || scopeKind == model.ScopeAgent) && scopeID != principal.ID && !principalHasAuthority(principal, authz.AuthorityPolicyAdmin) {
		return model.MemoryRecordV2{}, authz.ErrUnauthorized
	}
	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryRemember, scopeID, model.MemoryCandidate); err != nil {
		return model.MemoryRecordV2{}, err
	}
	if scopeKind == model.ScopeTask {
		if err := s.authorizeTaskScope(ctx, principal, authz.ActionMemoryRemember, scopeID); err != nil {
			return model.MemoryRecordV2{}, err
		}
	}
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
		Scope:       string(scopeKind),
		ScopeID:     scopeID,
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
	if err := s.IndexRecord(ctx, rec); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("index remembered memory: %w", err)
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
	existing, err := s.store.GetMemoryV2(ctx, req.ProjectID, req.MemoryID)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if req.ScopeID != "" && req.ScopeID != existing.ScopeID {
		return model.MemoryRecordV2{}, authz.ErrUnauthorized
	}
	if (model.MemoryScopeKind(existing.Scope) == model.ScopeOperatorPrivate || model.MemoryScopeKind(existing.Scope) == model.ScopeAgent) && existing.ScopeID != principal.ID && !principalHasAuthority(principal, authz.AuthorityPolicyAdmin) {
		return model.MemoryRecordV2{}, authz.ErrUnauthorized
	}
	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryPromote, existing.ScopeID, model.MemoryDurable); err != nil {
		return model.MemoryRecordV2{}, err
	}
	if existing.Lifecycle != model.MemoryCandidate && existing.Lifecycle != model.MemoryVerified {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: only candidate or verified memory can be promoted", model.ErrConflict)
	}
	if model.MemoryScopeKind(existing.Scope) == model.ScopeTask {
		if err := s.authorizeTaskScope(ctx, principal, authz.ActionMemoryPromote, existing.ScopeID); err != nil {
			return model.MemoryRecordV2{}, err
		}
	}
	// Operator-authored runtime_service candidates are explicit operator
	// proposals. Every model/import/outcome candidate remains evidence-gated,
	// even when the promoting principal happens to share its source identity.
	if existing.Authority == model.AuthorityAgent && existing.Source.Kind != "runtime_service" {
		if len(existing.EvidenceIDs) == 0 {
			return model.MemoryRecordV2{}, fmt.Errorf("%w: agent candidate promotion requires evidence", model.ErrInvalid)
		}
		for _, evidenceID := range existing.EvidenceIDs {
			node, err := s.store.Get(ctx, evidence.NodeID(evidenceID))
			if err != nil || (node.State != evidence.StateStored && node.State != evidence.StateLinked) {
				return model.MemoryRecordV2{}, fmt.Errorf("%w: promotion evidence %s is unavailable", model.ErrInvalid, evidenceID)
			}
		}
	}
	promoted, err := s.store.UpdateMemory(ctx, existing.ProjectID, req.MemoryID, existing.Revision, func(rec *model.MemoryRecordV2) error {
		rec.Lifecycle = model.MemoryDurable
		rec.Authority = model.AuthorityOperator
		now := time.Now().UTC()
		rec.LastVerifiedAt = &now
		if rec.ExtMeta == nil {
			rec.ExtMeta = map[string]any{}
		}
		rec.ExtMeta["promoted_by"] = principal.ID
		rec.ExtMeta["promotion_rationale"] = truncateMemoryField(req.Rationale, 2048)
		return nil
	})
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if err := s.IndexRecord(ctx, promoted); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("index promoted memory: %w", err)
	}
	return promoted, nil
}

type ExtractCandidateRequest struct {
	ProjectID   string                `json:"project_id"`
	TaskID      string                `json:"task_id,omitempty"`
	SessionID   string                `json:"session_id,omitempty"`
	RunID       string                `json:"run_id,omitempty"`
	Kind        model.MemoryKind      `json:"kind"`
	Title       string                `json:"title"`
	Body        string                `json:"body"`
	Scope       model.MemoryScopeKind `json:"scope"`
	ScopeID     string                `json:"scope_id"`
	EvidenceIDs []string              `json:"evidence_ids,omitempty"`
	HeadCommit  string                `json:"head_commit,omitempty"`
	BranchName  string                `json:"branch_name,omitempty"`
	WorktreeID  string                `json:"worktree_id,omitempty"`
	ExtMeta     map[string]any        `json:"ext_meta,omitempty"`
}

func (s *MemoryService) ExtractCandidate(ctx context.Context, principal authz.Principal, req ExtractCandidateRequest) (model.MemoryRecordV2, error) {
	if err := ctx.Err(); err != nil {
		return model.MemoryRecordV2{}, err
	}
	if s == nil || s.store == nil {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: memory store is unavailable", model.ErrUnavailable)
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: project_id is required", model.ErrInvalid)
	}
	scopeKind := req.Scope
	if scopeKind == "" {
		scopeKind = model.ScopeProject
	}
	scopeID := strings.TrimSpace(req.ScopeID)
	if scopeID == "" && scopeKind == model.ScopeProject {
		scopeID = req.ProjectID
	}
	if _, err := model.NewMemoryScope(string(scopeKind), scopeID); err != nil {
		return model.MemoryRecordV2{}, err
	}
	if scopeKind == model.ScopeProject && scopeID != req.ProjectID {
		return model.MemoryRecordV2{}, authz.ErrUnauthorized
	}
	if (scopeKind == model.ScopeOperatorPrivate || scopeKind == model.ScopeAgent) && scopeID != principal.ID && !principalHasAuthority(principal, authz.AuthorityPolicyAdmin) {
		return model.MemoryRecordV2{}, authz.ErrUnauthorized
	}
	if req.TaskID != "" && scopeKind == model.ScopeTask && scopeID != req.TaskID {
		return model.MemoryRecordV2{}, authz.ErrUnauthorized
	}
	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryRemember, scopeID, model.MemoryCandidate); err != nil {
		return model.MemoryRecordV2{}, err
	}
	if scopeKind == model.ScopeTask {
		if err := s.authorizeTaskScope(ctx, principal, authz.ActionMemoryRemember, scopeID); err != nil {
			return model.MemoryRecordV2{}, err
		}
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
		Authority:   model.AuthorityAgent, // Invariant: candidate extracts are ALWAYS agent-level authority
		Title:       req.Title,
		Body:        req.Body,
		Scope:       string(scopeKind),
		ScopeID:     scopeID,
		EvidenceIDs: req.EvidenceIDs,
		HeadCommit:  req.HeadCommit,
		BranchName:  req.BranchName,
		WorktreeID:  req.WorktreeID,
		SessionID:   req.SessionID,
		RunID:       req.RunID,
		ObservedAt:  now,
		IngestedAt:  now,
		ValidFrom:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		ExtMeta:     req.ExtMeta,
		Source: model.MemorySource{
			Kind:      "extracted_candidate",
			Reference: principal.ID,
			AgentID:   principal.ID,
			SessionID: req.SessionID,
			RunID:     req.RunID,
		},
	}
	if err := security.NewFirewall(security.FirewallConfig{}).ScanRecord(ctx, rec); err != nil {
		return model.MemoryRecordV2{}, err
	}

	// 1. Exact semantic deduplication by content in same scope
	existingList, err := s.store.ListMemoryV2(ctx, store.MemoryQueryFilter{
		ProjectID: req.ProjectID, Scope: scopeKind, ScopeID: scopeID, ActorID: principal.ID,
	})
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	for _, ex := range existingList {
		if ex.ACLScope != "" && ex.ACLScope != principal.ID {
			continue
		}
		if ex.Kind == rec.Kind && ex.Title == rec.Title && ex.Body == rec.Body && ex.Lifecycle != model.MemoryTombstoned {
			return ex, nil
		}
	}

	// 2. Conflict detection against existing active records
	for _, ex := range existingList {
		if ex.ACLScope != "" && ex.ACLScope != principal.ID {
			continue
		}
		if ex.Lifecycle == model.MemoryTombstoned || ex.Lifecycle == model.MemorySuperseded || ex.Kind == model.MemoryKindWorking {
			continue
		}
		if isConflict, reason := s.conflictDetector.DetectConflict(ctx, rec, ex); isConflict {
			rec.Lifecycle = model.MemoryConflicted
			rec.ConflictIDs = appendIfMissing(rec.ConflictIDs, ex.ID)
			if rec.ExtMeta == nil {
				rec.ExtMeta = map[string]any{}
			}
			rec.ExtMeta["conflict_reason"] = reason

			if ex.Lifecycle != model.MemoryConflicted {
				updated, updateErr := s.store.UpdateMemory(ctx, ex.ProjectID, ex.ID, ex.Revision, func(r *model.MemoryRecordV2) error {
					r.Lifecycle = model.MemoryConflicted
					r.ConflictIDs = appendIfMissing(r.ConflictIDs, rec.ID)
					return nil
				})
				if updateErr != nil {
					return model.MemoryRecordV2{}, updateErr
				}
				if err := s.IndexRecord(ctx, updated); err != nil {
					return model.MemoryRecordV2{}, fmt.Errorf("index conflicted memory: %w", err)
				}
			}
		}
	}

	if err := s.store.WriteMemoryV2(ctx, rec); err != nil {
		return model.MemoryRecordV2{}, err
	}
	if err := s.IndexRecord(ctx, rec); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("index extracted memory: %w", err)
	}
	return rec, nil
}

// CaptureOutcome persists a provider-neutral, evidence-linked candidate. It
// deliberately records deterministic run facts, not provider reasoning or raw
// transcript content. Promotion remains a separate governed operation.
func (s *MemoryService) CaptureOutcome(ctx context.Context, req OutcomeCaptureRequest) (model.MemoryRecordV2, error) {
	if err := ctx.Err(); err != nil {
		return model.MemoryRecordV2{}, err
	}
	if s == nil || s.store == nil {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: memory store is unavailable", model.ErrUnavailable)
	}
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.TaskID) == "" || strings.TrimSpace(req.RunID) == "" {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: project, task, and run are required", model.ErrInvalid)
	}
	now := time.Now().UTC()
	kind := model.MemoryKindEpisodic
	if req.Status != "success" {
		kind = model.MemoryKindFailure
	}
	metadata := map[string]any{
		"outcome_status": req.Status, "exit_status": req.ExitStatus,
		"files_changed":   boundedStrings(req.FilesChanged, 256, 1024),
		"tests_run":       boundedStrings(req.TestsRun, 128, 2048),
		"error_signature": truncateMemoryField(req.ErrorSignature, 512),
		"failure_reason":  truncateMemoryField(req.FailureReason, 2048),
		"retry_condition": truncateMemoryField(req.RetryCondition, 2048),
		"environment":     req.Environment,
	}
	body := fmt.Sprintf("task_id=%s provider=%s status=%s exit_status=%d base_commit=%s result_commit=%s", req.TaskID, req.Provider, req.Status, req.ExitStatus, req.BaseCommit, req.HeadCommit)
	if req.Status != "success" {
		body += fmt.Sprintf(" error_signature=%s failure_reason=%s retry_condition=%s", metadata["error_signature"], metadata["failure_reason"], metadata["retry_condition"])
	}
	rec := model.MemoryRecordV2{
		ID:         "MEM-RUN-" + req.RunID,
		ProjectID:  req.ProjectID,
		Kind:       kind,
		Lifecycle:  model.MemoryCandidate,
		Confidence: model.ConfidenceObserved,
		Authority:  model.AuthorityAgent,
		Title:      fmt.Sprintf("Run %s outcome: %s", req.Status, req.TaskTitle),
		Body:       body,
		Scope:      string(model.ScopeTask),
		ScopeID:    req.TaskID,
		Source: model.MemorySource{
			Kind: "runtime_outcome", Reference: req.RunID, AgentID: req.AgentID,
			SessionID: req.SessionID, RunID: req.RunID,
		},
		EvidenceIDs: req.EvidenceIDs,
		HeadCommit:  req.HeadCommit,
		BranchName:  req.Branch,
		SessionID:   req.SessionID,
		RunID:       req.RunID,
		ObservedAt:  now,
		IngestedAt:  now,
		ValidFrom:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		ExtMeta:     metadata,
	}
	if err := s.store.WriteMemoryV2(ctx, rec); err != nil {
		return model.MemoryRecordV2{}, err
	}
	if err := s.IndexRecord(ctx, rec); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("index run outcome: %w", err)
	}
	if err := s.store.LinkRetrievalReceiptsForRun(ctx, req.ProjectID, req.RunID, rec.ID, req.Status, req.EvidenceIDs); err != nil {
		return model.MemoryRecordV2{}, err
	}
	return rec, nil
}

func boundedStrings(values []string, maxItems, maxLength int) []string {
	if len(values) > maxItems {
		values = values[:maxItems]
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, truncateMemoryField(value, maxLength))
		}
	}
	return result
}

func truncateMemoryField(value string, maxLength int) string {
	if maxLength > 0 && len(value) > maxLength {
		return value[:maxLength]
	}
	return value
}

type FreshnessClassification string

const (
	FreshnessFresh         FreshnessClassification = "fresh"
	FreshnessPossiblyStale FreshnessClassification = "possibly_stale"
	FreshnessStale         FreshnessClassification = "stale"
	FreshnessExpired       FreshnessClassification = "expired"
	FreshnessSuperseded    FreshnessClassification = "superseded"
	FreshnessConflicted    FreshnessClassification = "conflicted"
	FreshnessUnverifiable  FreshnessClassification = "unverifiable"
)

type FreshnessEvaluation struct {
	MemoryID       string                  `json:"memory_id"`
	Classification FreshnessClassification `json:"classification"`
	Reason         string                  `json:"reason"`
	ScorePenalty   int                     `json:"score_penalty"`
}

type MemoryReconcileRequest struct {
	ProjectID         string            `json:"project_id"`
	CurrentHead       string            `json:"current_head"`
	CurrentBranch     string            `json:"current_branch"`
	ModifiedFiles     []string          `json:"modified_files,omitempty"`
	DeletedFiles      []string          `json:"deleted_files,omitempty"`
	RenamedFiles      map[string]string `json:"renamed_files,omitempty"`
	CurrentFileHashes map[string]string `json:"current_file_hashes,omitempty"`
	ExistingSymbols   map[string]bool   `json:"existing_symbols,omitempty"`
	InvalidatedTests  []string          `json:"invalidated_tests,omitempty"`
}

type MemoryReconcileReport struct {
	ProjectID   string                `json:"project_id"`
	Evaluations []FreshnessEvaluation `json:"evaluations"`
	StaleCount  int                   `json:"stale_count"`
	FreshCount  int                   `json:"fresh_count"`
}

func (s *MemoryService) EvaluateFreshness(rec model.MemoryRecordV2, req MemoryReconcileRequest) FreshnessEvaluation {
	if rec.Lifecycle == model.MemoryConflicted || len(rec.ConflictIDs) > 0 {
		return FreshnessEvaluation{MemoryID: rec.ID, Classification: FreshnessConflicted, Reason: "record has an unresolved semantic conflict", ScorePenalty: 1000}
	}
	// 1. Expired check
	if rec.ValidTo != nil && !rec.ValidTo.IsZero() && rec.ValidTo.Before(time.Now().UTC()) {
		return FreshnessEvaluation{
			MemoryID:       rec.ID,
			Classification: FreshnessExpired,
			Reason:         "valid_to expiration interval has passed",
			ScorePenalty:   1000,
		}
	}

	// 2. Superseded check
	if rec.Lifecycle == model.MemorySuperseded || len(rec.SupersededBy) > 0 {
		return FreshnessEvaluation{
			MemoryID:       rec.ID,
			Classification: FreshnessSuperseded,
			Reason:         "superseded by newer verified record",
			ScorePenalty:   1000,
		}
	}

	// 3. Explicit stale lifecycle
	if rec.Lifecycle == model.MemoryStale {
		return FreshnessEvaluation{
			MemoryID:       rec.ID,
			Classification: FreshnessStale,
			Reason:         "lifecycle marked as stale",
			ScorePenalty:   500,
		}
	}

	// 4. File-grounding checks (ext_meta file_path / referenced_files)
	var referencedFiles []string
	if rec.ExtMeta != nil {
		if fp, ok := rec.ExtMeta["file_path"].(string); ok && fp != "" {
			referencedFiles = append(referencedFiles, fp)
		}
		referencedFiles = append(referencedFiles, stringSliceMeta(rec.ExtMeta["referenced_files"])...)
	}
	for oldPath, newPath := range req.RenamedFiles {
		for _, referenced := range referencedFiles {
			if referenced == oldPath {
				return FreshnessEvaluation{MemoryID: rec.ID, Classification: FreshnessPossiblyStale, Reason: fmt.Sprintf("referenced file %s was renamed to %s", oldPath, newPath), ScorePenalty: 30}
			}
		}
	}
	for _, df := range req.DeletedFiles {
		for _, rf := range referencedFiles {
			if rf == df || strings.HasPrefix(rf, df) {
				return FreshnessEvaluation{
					MemoryID:       rec.ID,
					Classification: FreshnessStale,
					Reason:         fmt.Sprintf("referenced file %s was deleted from repository", rf),
					ScorePenalty:   500,
				}
			}
		}
	}
	for _, mf := range req.ModifiedFiles {
		for _, rf := range referencedFiles {
			if rf == mf || strings.HasPrefix(rf, mf) {
				return FreshnessEvaluation{
					MemoryID:       rec.ID,
					Classification: FreshnessPossiblyStale,
					Reason:         fmt.Sprintf("referenced file %s was modified in current worktree", rf),
					ScorePenalty:   20,
				}
			}
		}
	}
	if rec.ExtMeta != nil {
		recordedHashes := stringMapMeta(rec.ExtMeta["file_hashes"])
		for path, recordedHash := range recordedHashes {
			currentHash, known := req.CurrentFileHashes[path]
			if !known {
				continue
			}
			if currentHash != recordedHash {
				return FreshnessEvaluation{MemoryID: rec.ID, Classification: FreshnessStale, Reason: fmt.Sprintf("content hash changed for %s", path), ScorePenalty: 500}
			}
		}
		for _, symbol := range stringSliceMeta(rec.ExtMeta["symbols"]) {
			if exists, known := req.ExistingSymbols[symbol]; known && !exists {
				return FreshnessEvaluation{MemoryID: rec.ID, Classification: FreshnessStale, Reason: fmt.Sprintf("referenced symbol %s no longer exists", symbol), ScorePenalty: 500}
			}
		}
		invalidTests := make(map[string]struct{}, len(req.InvalidatedTests))
		for _, test := range req.InvalidatedTests {
			invalidTests[test] = struct{}{}
		}
		for _, test := range stringSliceMeta(rec.ExtMeta["verified_tests"]) {
			if _, invalid := invalidTests[test]; invalid {
				return FreshnessEvaluation{MemoryID: rec.ID, Classification: FreshnessPossiblyStale, Reason: fmt.Sprintf("supporting test %s was invalidated", test), ScorePenalty: 30}
			}
		}
	}

	// 5. Commit anchor checks
	if rec.HeadCommit != "" && req.CurrentHead != "" {
		if rec.HeadCommit == req.CurrentHead {
			return FreshnessEvaluation{
				MemoryID:       rec.ID,
				Classification: FreshnessFresh,
				Reason:         "head commit matches anchor exactly",
			}
		}
		// If branch diverged
		if rec.BranchName != "" && req.CurrentBranch != "" && rec.BranchName != req.CurrentBranch {
			return FreshnessEvaluation{
				MemoryID:       rec.ID,
				Classification: FreshnessPossiblyStale,
				Reason:         fmt.Sprintf("anchored to branch %s, current branch is %s", rec.BranchName, req.CurrentBranch),
				ScorePenalty:   20,
			}
		}
		// Episodic/run-outcome records tied to a specific commit become stale when commit advances
		if rec.Kind == model.MemoryKindEpisodic || rec.Kind == model.MemoryKindFailure {
			return FreshnessEvaluation{
				MemoryID:       rec.ID,
				Classification: FreshnessStale,
				Reason:         fmt.Sprintf("anchored to commit %s, head advanced to %s", rec.HeadCommit, req.CurrentHead),
				ScorePenalty:   500,
			}
		}
		if len(referencedFiles) > 0 {
			return FreshnessEvaluation{MemoryID: rec.ID, Classification: FreshnessPossiblyStale, Reason: "repository HEAD advanced for code-linked memory", ScorePenalty: 30}
		}
	}
	if len(referencedFiles) > 0 && rec.HeadCommit == "" {
		return FreshnessEvaluation{MemoryID: rec.ID, Classification: FreshnessUnverifiable, Reason: "code-linked memory has no repository commit anchor", ScorePenalty: 30}
	}

	// 6. Invariant architectural decisions and durable semantic memories remain fresh across commit advances
	return FreshnessEvaluation{
		MemoryID:       rec.ID,
		Classification: FreshnessFresh,
		Reason:         "repository-invariant decision/constraint",
	}
}

func stringSliceMeta(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok && text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func stringMapMeta(value any) map[string]string {
	result := map[string]string{}
	switch values := value.(type) {
	case map[string]string:
		for key, value := range values {
			result[key] = value
		}
	case map[string]any:
		for key, value := range values {
			if text, ok := value.(string); ok {
				result[key] = text
			}
		}
	}
	return result
}

func (s *MemoryService) Reconcile(ctx context.Context, principal authz.Principal, req MemoryReconcileRequest) (MemoryReconcileReport, error) {
	if err := ctx.Err(); err != nil {
		return MemoryReconcileReport{}, err
	}
	if s == nil || s.store == nil {
		return MemoryReconcileReport{}, fmt.Errorf("%w: memory store is unavailable", model.ErrUnavailable)
	}
	records, err := s.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: req.ProjectID, ActorID: principal.ID})
	if err != nil {
		return MemoryReconcileReport{}, err
	}

	report := MemoryReconcileReport{
		ProjectID: req.ProjectID,
	}
	for _, rec := range records {
		ev := s.EvaluateFreshness(rec, req)
		report.Evaluations = append(report.Evaluations, ev)
		if ev.Classification == FreshnessStale || ev.Classification == FreshnessExpired {
			report.StaleCount++
		} else {
			report.FreshCount++
		}
	}
	return report, nil
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

	if req.MaxRecords <= 0 || req.MaxRecords > 20 {
		req.MaxRecords = 8
	}
	if req.MaxBytes <= 0 || req.MaxBytes > 64<<10 {
		req.MaxBytes = 12 << 10
	}
	if len(req.AllowedScopeIDs) == 0 {
		req.AllowedScopeIDs = []string{req.ProjectID, principal.ID}
	} else {
		seenScopes := make(map[string]struct{}, len(req.AllowedScopeIDs))
		validatedScopes := make([]string, 0, len(req.AllowedScopeIDs))
		for _, scopeID := range req.AllowedScopeIDs {
			scopeID = strings.TrimSpace(scopeID)
			if scopeID == "" {
				continue
			}
			if _, duplicate := seenScopes[scopeID]; duplicate {
				continue
			}
			if scopeID != req.ProjectID && scopeID != principal.ID && scopeID != req.CurrentBranch {
				if err := s.authorizeTaskScope(ctx, principal, authz.ActionMemoryRecall, scopeID); err != nil {
					return RecallResponse{}, err
				}
			}
			seenScopes[scopeID] = struct{}{}
			validatedScopes = append(validatedScopes, scopeID)
		}
		req.AllowedScopeIDs = validatedScopes
	}

	records, err := s.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: req.ProjectID, ActorID: principal.ID})
	if err != nil {
		return RecallResponse{}, err
	}

	allowedScopeMap := make(map[string]bool)
	for _, sc := range req.AllowedScopeIDs {
		if sc != "" {
			allowedScopeMap[sc] = true
		}
	}
	authorizedIDs := make(map[string]struct{})
	authorizedRecords := make([]model.MemoryRecordV2, 0, len(records))
	deniedScopeCount := 0
	for _, rec := range records {
		scope, scopeErr := model.NewMemoryScope(rec.Scope, rec.ScopeID)
		if scopeErr != nil || !scope.AllowsRead(req.ProjectID, principal.ID) ||
			(rec.ACLScope != "" && rec.ACLScope != principal.ID) || !allowedScopeMap[rec.ScopeID] {
			deniedScopeCount++
			continue
		}
		authorizedIDs[rec.ID] = struct{}{}
		authorizedRecords = append(authorizedRecords, rec)
	}
	records = authorizedRecords

	query := strings.ToLower(strings.TrimSpace(req.Query))
	queryTerms := strings.Fields(query)
	s.mu.RLock()
	lexicalIndex := s.lexicalIndex
	graphIndex := s.graphIndex
	providers := append([]CandidateProvider(nil), s.providers...)
	s.mu.RUnlock()

	// Multi-track candidate discovery across derived projections
	matchedTrackMap := make(map[string][]string) // memoryID -> tracks
	scoreMap := make(map[string]int)
	cacheKey := cache.QueryKey{ProjectID: req.ProjectID, ScopeIDs: req.AllowedScopeIDs, Query: query, TopK: req.MaxRecords}
	if cached, ok := s.cache.Get(cacheKey); ok {
		for _, rec := range cached {
			if _, allowed := authorizedIDs[rec.ID]; !allowed {
				continue
			}
			matchedTrackMap[rec.ID] = appendIfMissing(matchedTrackMap[rec.ID], "cache")
		}
	}

	// 1. Lexical track candidates
	if lexicalIndex != nil && query != "" {
		trackCtx, cancel := context.WithTimeout(ctx, derivedTrackTimeout)
		lexResults, err := lexicalIndex.SearchAuthorized(trackCtx, req.ProjectID, query, authorizedIDs, lexicalCandidateLimit)
		cancel()
		if err == nil {
			for _, r := range lexResults {
				matchedTrackMap[r.MemoryID] = appendIfMissing(matchedTrackMap[r.MemoryID], "lexical")
				scoreMap[r.MemoryID] += int(r.Score)
			}
		}
	}

	// 2. Graph neighbor track candidates
	if graphIndex != nil && query != "" {
		var seeds []string
		for _, rec := range records {
			if strings.Contains(strings.ToLower(rec.ID), query) || strings.Contains(strings.ToLower(rec.Title), query) {
				seeds = append(seeds, rec.ID)
			}
		}
		if len(seeds) > 0 {
			trackCtx, cancel := context.WithTimeout(ctx, derivedTrackTimeout)
			nodes, _, err := graphIndex.Traverse(trackCtx, seeds, req.AllowedScopeIDs, time.Now().UTC(), graphTraversalDepth)
			cancel()
			if err == nil {
				for _, n := range nodes {
					if _, allowed := authorizedIDs[n.ID]; !allowed {
						continue
					}
					matchedTrackMap[n.ID] = appendIfMissing(matchedTrackMap[n.ID], "graph")
					scoreMap[n.ID] += 15
				}
			}
		}
	}

	// A vector backend is queried only by a registered provider that can also
	// produce a real query embedding. The built-in runtime has no fake empty-
	// embedding semantic path.

	// 4. Custom Candidate Providers (if registered)
	for _, prov := range providers {
		trackCtx, cancel := context.WithTimeout(ctx, derivedTrackTimeout)
		cands, err := prov.QueryCandidates(trackCtx, req.ProjectID, req.AllowedScopeIDs, query, providerCandidateLimit)
		cancel()
		if err == nil {
			for _, c := range cands {
				if _, allowed := authorizedIDs[c.MemoryID]; !allowed {
					continue
				}
				matchedTrackMap[c.MemoryID] = appendIfMissing(matchedTrackMap[c.MemoryID], c.Track)
				scoreMap[c.MemoryID] += int(c.Score)
			}
		}
	}

	type rankedRecord struct {
		record        model.MemoryRecordV2
		score         int
		authorityRank int
		tracks        []string
	}
	var ranked []rankedRecord
	receiptID, err := model.NewID("RCPT-")
	if err != nil {
		return RecallResponse{}, fmt.Errorf("create receipt ID: %w", err)
	}

	receipt := RetrievalReceipt{
		ReceiptID:       receiptID,
		Query:           req.Query,
		QueryDigest:     queryDigest(req.Query),
		ProjectID:       req.ProjectID,
		AllowedScopeIDs: append([]string(nil), req.AllowedScopeIDs...),
		CurrentHead:     req.CurrentHead,
		CurrentBranch:   req.CurrentBranch,
		MaxRecords:      req.MaxRecords,
		MaxBytes:        req.MaxBytes,
		RunID:           req.RunID,
		TaskID:          req.TaskID,
		Provider:        req.Provider,
		GeneratedAt:     time.Now().UTC(),
	}

	for _, rec := range records {
		decision := RetrievalDecision{MemoryID: rec.ID, Authority: string(rec.Authority), Lifecycle: string(rec.Lifecycle)}
		if rec.Kind == model.MemoryKindWorking || rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemoryRejected {
			decision.Reason = "inactive_lifecycle"
			receipt.Decisions = append(receipt.Decisions, decision)
			continue
		}

		// Evaluate freshness dynamically against current repository head and branch
		freshness := s.EvaluateFreshness(rec, MemoryReconcileRequest{
			ProjectID:         req.ProjectID,
			CurrentHead:       req.CurrentHead,
			CurrentBranch:     req.CurrentBranch,
			ModifiedFiles:     req.ModifiedFiles,
			DeletedFiles:      req.DeletedFiles,
			RenamedFiles:      req.RenamedFiles,
			CurrentFileHashes: req.CurrentFileHashes,
			ExistingSymbols:   req.ExistingSymbols,
			InvalidatedTests:  req.InvalidatedTests,
		})
		if freshness.Classification == FreshnessExpired {
			decision.Reason = "expired: " + freshness.Reason
			receipt.Decisions = append(receipt.Decisions, decision)
			continue
		}
		if freshness.Classification == FreshnessSuperseded {
			decision.Reason = "superseded: " + freshness.Reason
			receipt.Decisions = append(receipt.Decisions, decision)
			continue
		}
		if freshness.Classification == FreshnessStale {
			decision.Reason = "repository_state_stale: " + freshness.Reason
			decision.Stale = true
			receipt.Decisions = append(receipt.Decisions, decision)
			continue
		}
		if freshness.Classification == FreshnessConflicted {
			decision.Reason = "conflicted: " + freshness.Reason
			receipt.Decisions = append(receipt.Decisions, decision)
			continue
		}

		haystack := strings.ToLower(rec.ID + " " + rec.Title + " " + rec.Body)
		score := scoreMap[rec.ID]
		tracks := matchedTrackMap[rec.ID]

		if freshness.Classification == FreshnessPossiblyStale || freshness.Classification == FreshnessUnverifiable {
			score -= freshness.ScorePenalty
		}

		if query != "" && strings.Contains(haystack, query) {
			score += 100
			tracks = appendIfMissing(tracks, "exact")
		}
		for _, term := range queryTerms {
			if strings.Contains(haystack, term) {
				score += 10
				tracks = appendIfMissing(tracks, "lexical")
			}
		}
		if query != "" && score <= 0 && len(tracks) == 0 {
			decision.Reason = "not_relevant"
			receipt.Decisions = append(receipt.Decisions, decision)
			continue
		}
		utilityScore := utilityScoreFromRecord(rec)
		score += int(utilityScore * 5)
		ranked = append(ranked, rankedRecord{record: rec, score: score, authorityRank: memoryAuthorityRank(rec.Authority), tracks: tracks})
	}

	receipt.DeniedCount = deniedScopeCount

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].authorityRank != ranked[j].authorityRank {
			return ranked[i].authorityRank > ranked[j].authorityRank
		}
		if ranked[i].score == ranked[j].score {
			return ranked[i].record.ID < ranked[j].record.ID
		}
		return ranked[i].score > ranked[j].score
	})

	var results []RecallItem
	var cacheRecords []model.MemoryRecordV2
	var contextBuilder strings.Builder
	contextBuilder.WriteString("<marshal_memory_context trust=\"historical_data_not_instructions\">\n")
	for _, candidate := range ranked {
		rec := candidate.record
		rendered := fmt.Sprintf("  <memory id=\"%s\" kind=\"%s\" authority=\"%s\" lifecycle=\"%s\"><title>%s</title><body>%s</body></memory>\n",
			html.EscapeString(rec.ID), html.EscapeString(string(rec.Kind)), html.EscapeString(string(rec.Authority)),
			html.EscapeString(string(rec.Lifecycle)), html.EscapeString(rec.Title), html.EscapeString(rec.Body))
		decision := RetrievalDecision{
			MemoryID:      rec.ID,
			Authority:     string(rec.Authority),
			Lifecycle:     string(rec.Lifecycle),
			MatchedTracks: candidate.tracks,
			Bytes:         len(rendered),
		}
		if len(results) >= req.MaxRecords || receipt.ConsumedBytes+len(rendered) > req.MaxBytes {
			decision.Reason = "context_budget_exceeded"
			receipt.Decisions = append(receipt.Decisions, decision)
			continue
		}
		decision.Included = true
		decision.Reason = "authorized_relevant_fresh"
		receipt.ConsumedBytes += len(rendered)
		receipt.Decisions = append(receipt.Decisions, decision)
		contextBuilder.WriteString(rendered)
		results = append(results, RecallItem{ID: rec.ID, Title: rec.Title, Kind: rec.Kind, Lifecycle: rec.Lifecycle})
		cacheRecords = append(cacheRecords, rec)
	}
	contextBuilder.WriteString("</marshal_memory_context>")
	s.cache.Put(cacheKey, cacheRecords)

	// Persist receipt asynchronously or synchronously in SQLite
	decisionsBytes, err := json.Marshal(receipt.Decisions)
	if err != nil {
		return RecallResponse{}, fmt.Errorf("encode retrieval receipt: %w", err)
	}
	if err := s.store.WriteRetrievalReceipt(ctx, store.RetrievalReceiptRecord{
		ReceiptID:     receipt.ReceiptID,
		ProjectID:     req.ProjectID,
		CallerID:      principal.ID,
		QueryText:     "",
		QueryDigest:   receipt.QueryDigest,
		AllowedScopes: req.AllowedScopeIDs,
		CurrentHead:   req.CurrentHead,
		CurrentBranch: req.CurrentBranch,
		MaxRecords:    req.MaxRecords,
		MaxBytes:      req.MaxBytes,
		ConsumedBytes: receipt.ConsumedBytes,
		DecisionsJSON: string(decisionsBytes),
		RunID:         req.RunID,
		TaskID:        req.TaskID,
		Provider:      req.Provider,
		DeniedCount:   receipt.DeniedCount,
		CreatedAt:     receipt.GeneratedAt,
	}); err != nil {
		return RecallResponse{}, fmt.Errorf("persist retrieval receipt: %w", err)
	}

	return RecallResponse{Results: results, Receipt: receipt, Context: contextBuilder.String()}, nil
}

func (s *MemoryService) GetReceipt(ctx context.Context, principal authz.Principal, projectID, receiptID string) (RetrievalReceipt, error) {
	if err := ctx.Err(); err != nil {
		return RetrievalReceipt{}, err
	}
	if s == nil || s.store == nil {
		return RetrievalReceipt{}, fmt.Errorf("%w: memory store is unavailable", model.ErrUnavailable)
	}
	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryRecall, projectID, model.MemoryDurable); err != nil {
		return RetrievalReceipt{}, err
	}

	rec, err := s.store.GetRetrievalReceipt(ctx, projectID, receiptID)
	if err != nil {
		return RetrievalReceipt{}, err
	}
	if rec.CallerID != principal.ID && !principalHasAuthority(principal, authz.AuthorityPolicyAdmin) {
		return RetrievalReceipt{}, authz.ErrUnauthorized
	}

	var decisions []RetrievalDecision
	if err := json.Unmarshal([]byte(rec.DecisionsJSON), &decisions); err != nil {
		return RetrievalReceipt{}, fmt.Errorf("decode retrieval receipt: %w", err)
	}

	return RetrievalReceipt{
		ReceiptID:       rec.ReceiptID,
		Query:           "",
		QueryDigest:     rec.QueryDigest,
		ProjectID:       rec.ProjectID,
		AllowedScopeIDs: rec.AllowedScopes,
		CurrentHead:     rec.CurrentHead,
		CurrentBranch:   rec.CurrentBranch,
		MaxRecords:      rec.MaxRecords,
		MaxBytes:        rec.MaxBytes,
		ConsumedBytes:   rec.ConsumedBytes,
		Decisions:       decisions,
		RunID:           rec.RunID,
		TaskID:          rec.TaskID,
		Provider:        rec.Provider,
		EvidenceIDs:     append([]string(nil), rec.EvidenceIDs...),
		OutcomeMemoryID: rec.OutcomeMemoryID,
		OutcomeStatus:   rec.OutcomeStatus,
		DeniedCount:     rec.DeniedCount,
		GeneratedAt:     rec.CreatedAt,
	}, nil
}

// PruneReceipts applies the configured operator retention decision. Memory
// tombstones never rewrite historical receipts; receipts remain caller-bound
// until an explicit policy-admin retention operation removes them.
func (s *MemoryService) PruneReceipts(ctx context.Context, principal authz.Principal, projectID string, before time.Time) (int64, error) {
	if !principalHasAuthority(principal, authz.AuthorityPolicyAdmin) {
		return 0, authz.ErrUnauthorized
	}
	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryTombstone, projectID, model.MemoryTombstoned); err != nil {
		return 0, err
	}
	return s.store.PruneRetrievalReceipts(ctx, projectID, before)
}

func queryDigest(query string) string {
	sum := sha256.Sum256([]byte(query))
	return hex.EncodeToString(sum[:])
}

func principalHasAuthority(principal authz.Principal, wanted authz.Authority) bool {
	for _, authority := range principal.Role.Authorities {
		if authority == wanted {
			return true
		}
	}
	return false
}

func (s *MemoryService) authorizeTaskScope(ctx context.Context, principal authz.Principal, action authz.MemoryAction, taskID string) error {
	if err := s.authorizer.Authorize(ctx, principal, action, taskID, model.MemoryCandidate); err != nil {
		return err
	}
	if principalHasAuthority(principal, authz.AuthorityPolicyAdmin) {
		return nil
	}
	allowed, err := s.store.HasActiveRoleBinding(ctx, principal.ID, taskID)
	if err != nil {
		return err
	}
	if !allowed {
		allowed, err = s.store.HasActiveTaskSession(ctx, principal.ID, taskID)
		if err != nil {
			return err
		}
	}
	if !allowed {
		return authz.ErrUnauthorized
	}
	return nil
}

func memoryAuthorityRank(authority model.MemoryAuthority) int {
	switch authority {
	case model.AuthorityOperator:
		return 4
	case model.AuthorityPolicy:
		return 3
	case model.AuthorityVerified:
		return 2
	case model.AuthorityAgent:
		return 1
	default:
		return 0
	}
}

func utilityScoreFromRecord(rec model.MemoryRecordV2) float64 {
	if rec.ExtMeta == nil {
		return 0.5
	}
	successes, _ := numericMeta(rec.ExtMeta["utility_success_count"])
	failures, _ := numericMeta(rec.ExtMeta["utility_failure_count"])
	// The legacy success counter mirrors verification_contributing signals for
	// compatibility, so do not count that signal twice when ranking.
	for _, key := range []string{"utility_helpful_count", "utility_used_count"} {
		value, _ := numericMeta(rec.ExtMeta[key])
		successes += value
	}
	for _, key := range []string{"utility_ignored_count", "utility_contradicted_count"} {
		value, _ := numericMeta(rec.ExtMeta[key])
		failures += value
	}
	return (1 + successes) / (2 + successes + failures)
}

func numericMeta(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func appendIfMissing(slice []string, val string) []string {
	for _, item := range slice {
		if item == val {
			return slice
		}
	}
	return append(slice, val)
}

// Task Blackboard and Working Memory Methods (M14)

func (s *MemoryService) SetTaskSlot(ctx context.Context, principal authz.Principal, projectID, taskID string, slotType working.SlotType, value string, pinned bool) (working.WorkingSlot, error) {
	return s.SetTaskSlotWithProvenance(ctx, principal, projectID, taskID, slotType, value, pinned, WorkingProvenance{})
}

type WorkingProvenance struct {
	Provider  string `json:"provider,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
}

func (s *MemoryService) SetTaskSlotWithProvenance(ctx context.Context, principal authz.Principal, projectID, taskID string, slotType working.SlotType, value string, pinned bool, provenance WorkingProvenance) (working.WorkingSlot, error) {
	if err := ctx.Err(); err != nil {
		return working.WorkingSlot{}, err
	}
	if s == nil || s.store == nil {
		return working.WorkingSlot{}, fmt.Errorf("%w: memory store unavailable", model.ErrUnavailable)
	}
	if err := s.authorizeTaskScope(ctx, principal, authz.ActionMemoryRemember, taskID); err != nil {
		return working.WorkingSlot{}, err
	}
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(taskID) == "" || strings.TrimSpace(string(slotType)) == "" || strings.TrimSpace(value) == "" {
		return working.WorkingSlot{}, fmt.Errorf("%w: project, task, slot type, and value are required", model.ErrInvalid)
	}
	rec := newWorkingRecord(projectID, taskID, principal.ID, taskSlotID(projectID, taskID, string(slotType)), slotType, value, pinned, false)
	rec.Source.SessionID = truncateMemoryField(strings.TrimSpace(provenance.SessionID), 256)
	rec.Source.RunID = truncateMemoryField(strings.TrimSpace(provenance.RunID), 256)
	rec.ExtMeta["provider"] = truncateMemoryField(strings.TrimSpace(provenance.Provider), 128)
	rec.SessionID = rec.Source.SessionID
	rec.RunID = rec.Source.RunID
	if _, err := s.store.GetMemoryV2(ctx, projectID, rec.ID); err == nil {
		if conflictErr := s.persistWorkingConflict(ctx, projectID, taskID, principal.ID, slotType, value, rec.ID, provenance); conflictErr != nil {
			return working.WorkingSlot{}, fmt.Errorf("persist competing slot proposal: %w", conflictErr)
		}
		return working.WorkingSlot{}, fmt.Errorf("%w: slot already exists; use CAS update", working.ErrCASConflict)
	} else if !errors.Is(err, model.ErrNotFound) {
		return working.WorkingSlot{}, err
	}
	if err := s.store.WriteMemoryV2(ctx, rec); err != nil {
		if _, existsErr := s.store.GetMemoryV2(ctx, projectID, rec.ID); existsErr == nil {
			if conflictErr := s.persistWorkingConflict(ctx, projectID, taskID, principal.ID, slotType, value, rec.ID, provenance); conflictErr != nil {
				return working.WorkingSlot{}, fmt.Errorf("persist competing slot proposal: %w", conflictErr)
			}
			return working.WorkingSlot{}, fmt.Errorf("%w: concurrent slot creation", working.ErrCASConflict)
		}
		return working.WorkingSlot{}, err
	}
	if err := s.IndexRecord(ctx, rec); err != nil {
		return working.WorkingSlot{}, fmt.Errorf("index task slot: %w", err)
	}
	return workingSlotFromRecord(rec), nil
}

func (s *MemoryService) UpdateTaskSlotCAS(ctx context.Context, principal authz.Principal, projectID, taskID string, slotType working.SlotType, expectedRevision int, newValue string) (working.WorkingSlot, error) {
	return s.UpdateTaskSlotCASWithProvenance(ctx, principal, projectID, taskID, slotType, expectedRevision, newValue, WorkingProvenance{})
}

func (s *MemoryService) UpdateTaskSlotCASWithProvenance(ctx context.Context, principal authz.Principal, projectID, taskID string, slotType working.SlotType, expectedRevision int, newValue string, provenance WorkingProvenance) (working.WorkingSlot, error) {
	if err := ctx.Err(); err != nil {
		return working.WorkingSlot{}, err
	}
	if s == nil || s.store == nil {
		return working.WorkingSlot{}, fmt.Errorf("%w: memory store unavailable", model.ErrUnavailable)
	}
	if err := s.authorizeTaskScope(ctx, principal, authz.ActionMemoryRemember, taskID); err != nil {
		return working.WorkingSlot{}, err
	}
	if expectedRevision <= 0 || strings.TrimSpace(newValue) == "" {
		return working.WorkingSlot{}, fmt.Errorf("%w: positive expected revision and value are required", model.ErrInvalid)
	}
	id := taskSlotID(projectID, taskID, string(slotType))
	rec, err := s.store.UpdateMemory(ctx, projectID, id, int64(expectedRevision-1), func(rec *model.MemoryRecordV2) error {
		if !isWorkingRecord(rec, "task_slot") || rec.ScopeID != taskID {
			return model.ErrNotFound
		}
		rec.Body = newValue
		if rec.ExtMeta == nil {
			rec.ExtMeta = map[string]any{}
		}
		rec.ExtMeta["last_agent_id"] = principal.ID
		rec.ExtMeta["provider"] = truncateMemoryField(strings.TrimSpace(provenance.Provider), 128)
		rec.Source.AgentID = principal.ID
		rec.Source.SessionID = truncateMemoryField(strings.TrimSpace(provenance.SessionID), 256)
		rec.Source.RunID = truncateMemoryField(strings.TrimSpace(provenance.RunID), 256)
		rec.SessionID = rec.Source.SessionID
		rec.RunID = rec.Source.RunID
		return nil
	})
	if err != nil {
		if errors.Is(err, model.ErrConflict) {
			if conflictErr := s.persistWorkingConflict(ctx, projectID, taskID, principal.ID, slotType, newValue, id, provenance); conflictErr != nil {
				return working.WorkingSlot{}, fmt.Errorf("persist competing CAS proposal: %w", conflictErr)
			}
			return working.WorkingSlot{}, fmt.Errorf("%w: %v", working.ErrCASConflict, err)
		}
		if errors.Is(err, model.ErrNotFound) {
			return working.WorkingSlot{}, working.ErrSlotNotFound
		}
		return working.WorkingSlot{}, err
	}
	if err := s.IndexRecord(ctx, rec); err != nil {
		return working.WorkingSlot{}, fmt.Errorf("index updated task slot: %w", err)
	}
	return workingSlotFromRecord(rec), nil
}

func (s *MemoryService) ListTaskSlots(ctx context.Context, principal authz.Principal, projectID, taskID string) ([]working.WorkingSlot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("%w: memory store unavailable", model.ErrUnavailable)
	}
	if err := s.authorizeTaskScope(ctx, principal, authz.ActionMemoryRecall, taskID); err != nil {
		return nil, err
	}
	records, err := s.store.ListMemoryV2(ctx, store.MemoryQueryFilter{
		ProjectID: projectID, Kind: model.MemoryKindWorking, Scope: model.ScopeTask, ScopeID: taskID, ActorID: principal.ID,
	})
	if err != nil {
		return nil, err
	}
	slots := make([]working.WorkingSlot, 0, len(records))
	for _, rec := range records {
		if isWorkingRecord(&rec, "task_slot") {
			slots = append(slots, workingSlotFromRecord(rec))
		}
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].Type < slots[j].Type })
	return slots, nil
}

func (s *MemoryService) SetPrivateTaskSlot(ctx context.Context, principal authz.Principal, projectID, taskID, key, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return fmt.Errorf("%w: memory store unavailable", model.ErrUnavailable)
	}
	if err := s.authorizeTaskScope(ctx, principal, authz.ActionMemoryRemember, taskID); err != nil {
		return err
	}
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(taskID) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: project, task, key, and value are required", model.ErrInvalid)
	}
	id := privateSlotID(projectID, taskID, principal.ID, key)
	existing, err := s.store.GetMemoryV2(ctx, projectID, id)
	if errors.Is(err, model.ErrNotFound) {
		rec := newWorkingRecord(projectID, taskID, principal.ID, id, working.SlotType(key), value, false, true)
		if err := s.store.WriteMemoryV2(ctx, rec); err != nil {
			return err
		}
		if err := s.IndexRecord(ctx, rec); err != nil {
			return fmt.Errorf("index private slot: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	updated, err := s.store.UpdateMemory(ctx, projectID, id, existing.Revision, func(rec *model.MemoryRecordV2) error {
		if rec.ACLScope != principal.ID || !isWorkingRecord(rec, "private_slot") {
			return authz.ErrUnauthorized
		}
		rec.Body = value
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.IndexRecord(ctx, updated); err != nil {
		return fmt.Errorf("index updated private slot: %w", err)
	}
	return nil
}

func (s *MemoryService) GetPrivateTaskSlot(ctx context.Context, principal authz.Principal, projectID, taskID, key string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if s == nil || s.store == nil {
		return "", false, fmt.Errorf("%w: memory store unavailable", model.ErrUnavailable)
	}
	if err := s.authorizeTaskScope(ctx, principal, authz.ActionMemoryRecall, taskID); err != nil {
		return "", false, err
	}
	rec, err := s.store.GetMemoryV2(ctx, projectID, privateSlotID(projectID, taskID, principal.ID, key))
	if errors.Is(err, model.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if rec.ACLScope != principal.ID || rec.ScopeID != principal.ID || !isWorkingRecord(&rec, "private_slot") {
		return "", false, authz.ErrUnauthorized
	}
	return rec.Body, true, nil
}

func taskSlotID(projectID, taskID, slotType string) string {
	return deterministicMemoryID("MEM-WORK-", projectID, taskID, slotType)
}

func privateSlotID(projectID, taskID, principalID, key string) string {
	return deterministicMemoryID("MEM-PRIV-", projectID, taskID, principalID, key)
}

func deterministicMemoryID(prefix string, parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return prefix + hex.EncodeToString(h.Sum(nil))[:24]
}

func newWorkingRecord(projectID, taskID, agentID, id string, slotType working.SlotType, value string, pinned, private bool) model.MemoryRecordV2 {
	now := time.Now().UTC()
	recordType := "task_slot"
	scope, scopeID, acl := model.ScopeTask, taskID, ""
	if private {
		recordType, scope, scopeID, acl = "private_slot", model.ScopeOperatorPrivate, agentID, agentID
	}
	return model.MemoryRecordV2{
		ID: id, ProjectID: projectID, Kind: model.MemoryKindWorking, Lifecycle: model.MemoryObserved,
		Confidence: model.ConfidenceObserved, Authority: model.AuthorityAgent,
		Title: "Task working memory " + string(slotType), Body: value, Scope: string(scope), ScopeID: scopeID, ACLScope: acl,
		Source:     model.MemorySource{Kind: "runtime_working_memory", Reference: taskID, AgentID: agentID},
		ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now,
		ExtMeta: map[string]any{"record_type": recordType, "slot_type": string(slotType), "pinned": pinned, "last_agent_id": agentID, "task_id": taskID},
	}
}

func isWorkingRecord(rec *model.MemoryRecordV2, recordType string) bool {
	if rec == nil || rec.Kind != model.MemoryKindWorking || rec.ExtMeta == nil {
		return false
	}
	value, _ := rec.ExtMeta["record_type"].(string)
	return value == recordType
}

func workingSlotFromRecord(rec model.MemoryRecordV2) working.WorkingSlot {
	slotType, _ := rec.ExtMeta["slot_type"].(string)
	pinned, _ := rec.ExtMeta["pinned"].(bool)
	lastAgentID, _ := rec.ExtMeta["last_agent_id"].(string)
	provider, _ := rec.ExtMeta["provider"].(string)
	return working.WorkingSlot{Type: working.SlotType(slotType), Value: rec.Body, Revision: int(rec.Revision) + 1, Pinned: pinned, LastAgentID: lastAgentID, Provider: provider, SessionID: rec.Source.SessionID, RunID: rec.Source.RunID, UpdatedAt: rec.UpdatedAt}
}

func (s *MemoryService) persistWorkingConflict(ctx context.Context, projectID, taskID, agentID string, slotType working.SlotType, value, existingID string, provenance WorkingProvenance) error {
	id, err := model.NewID("MEM-CONFLICT-")
	if err != nil {
		return err
	}
	rec := newWorkingRecord(projectID, taskID, agentID, id, slotType, value, false, false)
	rec.Source.SessionID = truncateMemoryField(strings.TrimSpace(provenance.SessionID), 256)
	rec.Source.RunID = truncateMemoryField(strings.TrimSpace(provenance.RunID), 256)
	rec.SessionID = rec.Source.SessionID
	rec.RunID = rec.Source.RunID
	rec.ExtMeta["provider"] = truncateMemoryField(strings.TrimSpace(provenance.Provider), 128)
	rec.Lifecycle = model.MemoryConflicted
	rec.ConflictIDs = []string{existingID}
	rec.ExtMeta["record_type"] = "task_slot_conflict"
	return s.store.WriteMemoryV2(ctx, rec)
}

func (s *MemoryService) PromoteTaskSlot(ctx context.Context, principal authz.Principal, projectID, taskID string, slotType working.SlotType, targetKind model.MemoryKind, title string) (model.MemoryRecordV2, error) {
	if err := ctx.Err(); err != nil {
		return model.MemoryRecordV2{}, err
	}
	slots, err := s.ListTaskSlots(ctx, principal, projectID, taskID)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	var targetSlot *working.WorkingSlot
	for i := range slots {
		if slots[i].Type == slotType {
			targetSlot = &slots[i]
			break
		}
	}
	if targetSlot == nil {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: slot %s not found on task %s", model.ErrNotFound, slotType, taskID)
	}

	return s.ExtractCandidate(ctx, principal, ExtractCandidateRequest{
		ProjectID: projectID,
		TaskID:    taskID,
		Kind:      targetKind,
		Title:     title,
		Body:      targetSlot.Value,
		Scope:     model.ScopeTask,
		ScopeID:   taskID,
		ExtMeta: map[string]any{
			"source_slot_type": string(slotType),
			"slot_revision":    targetSlot.Revision,
			"promoted_by":      principal.ID,
		},
	})
}

// Provider-Neutral Handoff Surfaces (M15)

type HandoffCompileRequest struct {
	ProjectID     string   `json:"project_id"`
	TaskID        string   `json:"task_id"`
	SourceAgentID string   `json:"source_agent_id"`
	TargetRole    string   `json:"target_role,omitempty"`
	MaxBytes      int      `json:"max_bytes,omitempty"`
	CurrentHead   string   `json:"current_head,omitempty"`
	CurrentBranch string   `json:"current_branch,omitempty"`
	ChangedFiles  []string `json:"changed_files,omitempty"`
	DiffHash      string   `json:"diff_hash,omitempty"`
}

type HandoffBundle struct {
	BundleID       string                `json:"bundle_id"`
	TaskID         string                `json:"task_id"`
	ProjectID      string                `json:"project_id"`
	SourceAgentID  string                `json:"source_agent_id"`
	TargetRole     string                `json:"target_role"`
	TaskDefinition model.Task            `json:"task_definition"`
	WorkingSlots   []working.WorkingSlot `json:"working_slots"`
	MemoryContext  string                `json:"memory_context"`
	MemoryIDs      []string              `json:"memory_ids"`
	EvidenceIDs    []string              `json:"evidence_ids"`
	CurrentHead    string                `json:"current_head"`
	CurrentBranch  string                `json:"current_branch"`
	ChangedFiles   []string              `json:"changed_files"`
	DiffHash       string                `json:"diff_hash"`
	ByteSize       int                   `json:"byte_size"`
	GeneratedAt    time.Time             `json:"generated_at"`
}

func (s *MemoryService) CompileHandoff(ctx context.Context, principal authz.Principal, req HandoffCompileRequest) (HandoffBundle, error) {
	if err := ctx.Err(); err != nil {
		return HandoffBundle{}, err
	}
	if s == nil || s.store == nil {
		return HandoffBundle{}, fmt.Errorf("%w: memory store is unavailable", model.ErrUnavailable)
	}
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.TaskID) == "" {
		return HandoffBundle{}, fmt.Errorf("%w: project_id and task_id are required", model.ErrInvalid)
	}
	if err := s.authorizeTaskScope(ctx, principal, authz.ActionMemoryRecall, req.TaskID); err != nil {
		return HandoffBundle{}, err
	}
	if req.SourceAgentID == "" {
		req.SourceAgentID = principal.ID
	}
	if req.SourceAgentID != principal.ID {
		return HandoffBundle{}, authz.ErrUnauthorized
	}
	if req.MaxBytes <= 0 || req.MaxBytes > 128<<10 {
		req.MaxBytes = 32 << 10
	}

	// 1. Fetch Task Definition
	task, err := s.store.GetTask(ctx, req.TaskID)
	if err != nil {
		return HandoffBundle{}, fmt.Errorf("read task definition: %w", err)
	}

	// 2. Fetch Working Memory Slots for Task
	slots, err := s.ListTaskSlots(ctx, principal, req.ProjectID, req.TaskID)
	if err != nil {
		return HandoffBundle{}, fmt.Errorf("read task working memory: %w", err)
	}

	// 3. Recall memory context for task scope + project scope (Strictly without other agent private scopes)
	recallRes, err := s.Recall(ctx, principal, RecallRequest{
		ProjectID:       req.ProjectID,
		Query:           task.Title + " " + task.ID,
		AllowedScopeIDs: []string{req.ProjectID, req.TaskID, principal.ID},
		CurrentHead:     req.CurrentHead,
		CurrentBranch:   req.CurrentBranch,
		MaxRecords:      10,
		MaxBytes:        req.MaxBytes / 2,
		TaskID:          req.TaskID,
		Provider:        req.TargetRole,
	})
	if err != nil {
		return HandoffBundle{}, fmt.Errorf("recall handoff memory context: %w", err)
	}

	// 4. Extract evidence IDs
	evidenceMap := make(map[string]bool)
	for _, rec := range recallRes.Results {
		r, err := s.store.GetMemoryV2(ctx, req.ProjectID, rec.ID)
		if err != nil {
			return HandoffBundle{}, fmt.Errorf("reload handoff memory %s: %w", rec.ID, err)
		}
		for _, evID := range r.EvidenceIDs {
			if evID != "" {
				evidenceMap[evID] = true
			}
		}
	}
	var evidenceIDs []string
	var memoryIDs []string
	for _, item := range recallRes.Results {
		memoryIDs = append(memoryIDs, item.ID)
	}
	for evID := range evidenceMap {
		evidenceIDs = append(evidenceIDs, evID)
	}
	sort.Strings(evidenceIDs)

	bundleID, err := model.NewID("HND-")
	if err != nil {
		return HandoffBundle{}, fmt.Errorf("create handoff ID: %w", err)
	}

	bundle := HandoffBundle{
		BundleID:       bundleID,
		TaskID:         req.TaskID,
		ProjectID:      req.ProjectID,
		SourceAgentID:  req.SourceAgentID,
		TargetRole:     req.TargetRole,
		TaskDefinition: task,
		WorkingSlots:   slots,
		MemoryContext:  recallRes.Context,
		MemoryIDs:      memoryIDs,
		EvidenceIDs:    evidenceIDs,
		CurrentHead:    req.CurrentHead,
		CurrentBranch:  req.CurrentBranch,
		ChangedFiles:   req.ChangedFiles,
		DiffHash:       req.DiffHash,
		GeneratedAt:    time.Now().UTC(),
	}

	raw, err := json.Marshal(bundle)
	if err != nil {
		return HandoffBundle{}, fmt.Errorf("encode handoff bundle: %w", err)
	}
	bundle.ByteSize = len(raw)
	raw, err = json.Marshal(bundle)
	if err != nil {
		return HandoffBundle{}, fmt.Errorf("encode sized handoff bundle: %w", err)
	}
	bundle.ByteSize = len(raw)
	if err := security.NewFirewall(security.FirewallConfig{}).ScanText(string(raw)); err != nil {
		return HandoffBundle{}, fmt.Errorf("handoff firewall rejected: %w", err)
	}
	if bundle.ByteSize > req.MaxBytes {
		return HandoffBundle{}, fmt.Errorf("%w: handoff is %d bytes, limit is %d", model.ErrInvalid, bundle.ByteSize, req.MaxBytes)
	}
	return bundle, nil
}

// Retroactive Session Importer (M16)

func (s *MemoryService) ImportSessionTranscript(ctx context.Context, principal authz.Principal, projectID string, transcriptJSON []byte, dryRun bool) (importer.ImportResult, error) {
	if err := ctx.Err(); err != nil {
		return importer.ImportResult{}, err
	}
	if s == nil || s.store == nil || s.sessionImporter == nil {
		return importer.ImportResult{}, fmt.Errorf("%w: session importer is unavailable", model.ErrUnavailable)
	}
	if strings.TrimSpace(projectID) == "" {
		return importer.ImportResult{}, fmt.Errorf("%w: project_id is required", model.ErrInvalid)
	}
	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryRemember, projectID, model.MemoryCandidate); err != nil {
		return importer.ImportResult{}, err
	}

	result, err := s.sessionImporter.ImportRawJSON(ctx, projectID, transcriptJSON, dryRun)
	if err != nil {
		return importer.ImportResult{}, err
	}

	if !dryRun {
		var committed []model.MemoryRecordV2
		for _, rec := range result.ImportedRecords {
			// Check if already in SQLite
			if ex, err := s.store.FindMemoryByDigest(ctx, projectID, rec.ContentDigest); err == nil && ex.ID != "" {
				result.SkippedCount++
				continue
			}
			if err := s.store.WriteMemoryV2(ctx, rec); err != nil {
				return importer.ImportResult{}, fmt.Errorf("persist imported record: %w", err)
			}
			if err := s.IndexRecord(ctx, rec); err != nil {
				return importer.ImportResult{}, fmt.Errorf("index imported record: %w", err)
			}
			committed = append(committed, rec)
		}
		result.ImportedRecords = committed
	}

	return result, nil
}

// Outcome Utility and Lifecycle Consistency (M17)

type UtilitySignal string

const (
	UtilityRetrieved                UtilitySignal = "retrieved"
	UtilityIncluded                 UtilitySignal = "included"
	UtilityUsed                     UtilitySignal = "used"
	UtilityHelpful                  UtilitySignal = "helpful"
	UtilityIgnored                  UtilitySignal = "ignored"
	UtilityContradicted             UtilitySignal = "contradicted"
	UtilitySuperseded               UtilitySignal = "superseded"
	UtilityVerificationContributing UtilitySignal = "verification_contributing"
	UtilityFailed                   UtilitySignal = "failed"
)

func (signal UtilitySignal) valid() bool {
	switch signal {
	case UtilityRetrieved, UtilityIncluded, UtilityUsed, UtilityHelpful, UtilityIgnored,
		UtilityContradicted, UtilitySuperseded, UtilityVerificationContributing, UtilityFailed:
		return true
	default:
		return false
	}
}

func (s *MemoryService) RecordOutcome(ctx context.Context, memoryID, taskID string, success, operatorApproved bool) error {
	eventID, err := model.NewID("UTIL-")
	if err != nil {
		return err
	}
	signal := UtilityFailed
	if success {
		signal = UtilityVerificationContributing
	}
	return s.RecordUtilitySignal(ctx, memoryID, taskID, eventID, signal, operatorApproved)
}

func (s *MemoryService) RecordUtilitySignal(ctx context.Context, memoryID, taskID, eventID string, signal UtilitySignal, operatorApproved bool) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("%w: memory store unavailable", model.ErrUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if memoryID == "" || taskID == "" || eventID == "" || !signal.valid() {
		return fmt.Errorf("%w: memory, task, event, and valid utility signal are required", model.ErrInvalid)
	}
	for attempt := 0; attempt < 3; attempt++ {
		rec, err := s.store.GetMemoryV2ByID(ctx, memoryID)
		if err != nil {
			return err
		}
		updated, err := s.store.UpdateMemory(ctx, rec.ProjectID, rec.ID, rec.Revision, func(current *model.MemoryRecordV2) error {
			if current.Lifecycle == model.MemoryTombstoned || current.Lifecycle == model.MemorySuperseded ||
				(current.ValidTo != nil && current.ValidTo.Before(time.Now().UTC())) {
				return fmt.Errorf("%w: inactive memory cannot receive utility", model.ErrConflict)
			}
			if current.ExtMeta == nil {
				current.ExtMeta = map[string]any{}
			}
			events := stringMetaSlice(current.ExtMeta["utility_event_ids"])
			for _, existingEvent := range events {
				if existingEvent == eventID {
					return nil
				}
			}
			events = append(events, eventID)
			if len(events) > 256 {
				events = events[len(events)-256:]
			}
			current.ExtMeta["utility_event_ids"] = events
			key := "utility_" + string(signal) + "_count"
			count, _ := numericMeta(current.ExtMeta[key])
			if count < 1_000_000 {
				current.ExtMeta[key] = count + 1
			}
			if signal == UtilityVerificationContributing {
				current.ExtMeta["utility_success_count"] = minNumericMeta(current.ExtMeta["utility_success_count"], 1_000_000)
			}
			if signal == UtilityFailed {
				current.ExtMeta["utility_failure_count"] = minNumericMeta(current.ExtMeta["utility_failure_count"], 1_000_000)
			}
			current.ExtMeta["utility_last_task_id"] = taskID
			current.ExtMeta["utility_last_used_at"] = time.Now().UTC().Format(time.RFC3339Nano)
			// operatorApproved is intentionally not an authority escalation. It is
			// retained only as a bounded outcome signal.
			if operatorApproved {
				current.ExtMeta["utility_operator_approved"] = true
			}
			return nil
		})
		if err == nil {
			if err := s.IndexRecord(ctx, updated); err != nil {
				return fmt.Errorf("index utility update: %w", err)
			}
			return nil
		}
		if !errors.Is(err, model.ErrConflict) {
			return err
		}
	}
	return fmt.Errorf("%w: utility update remained contended", model.ErrConflict)
}

func stringMetaSlice(value any) []string {
	var result []string
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		for _, item := range values {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
	}
	return result
}

func minNumericMeta(value any, maximum float64) float64 {
	count, _ := numericMeta(value)
	if count >= maximum {
		return maximum
	}
	return count + 1
}

func (s *MemoryService) GetUtilityScore(ctx context.Context, memoryID string) float64 {
	if s == nil || s.store == nil {
		return 0.5
	}
	rec, err := s.store.GetMemoryV2ByID(ctx, memoryID)
	if err != nil {
		return 0.5
	}
	return utilityScoreFromRecord(rec)
}
