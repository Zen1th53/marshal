//go:build !governed

package app

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

const OperatorMemoryPageBytes = 2048
const operatorMemoryReadMaximumBytes = 256 << 10

// Only an authenticated, actual full reader creates this metadata capability.
// It has no JSON-restorable fields, body, source claims, or execution authority.
type OperatorMemoryRead struct {
	mu                                     sync.Mutex
	runtime                                *Runtime
	actor, id, digest, state               string
	revision                               int64
	next, size                             int
	expires                                time.Time
	complete, consumed, persisted, indexed bool
	finished                               bool
	promotedState, rationale               string
	promotedRevision                       int64
}

func (t *OperatorMemoryRead) Invalidate() {
	if t != nil {
		t.mu.Lock()
		t.consumed, t.complete = true, false
		t.mu.Unlock()
	}
}

type OperatorMemoryPage struct {
	ID, Title, Body, Lifecycle, Authority, Confidence, SourceKind string
	Revision                                                      int64
	Offset, NextOffset, BodyBytes, EvidenceCount                  int
	FullRead                                                      bool
	Withheld                                                      bool
}
type OperatorMemoryPromotion struct {
	ID                 string
	Revision           int64
	Persisted, Indexed bool
}
type operatorMemoryPromotionKey struct{}
type operatorMemoryDerivedIndexKey struct{}

// A private in-process fault observer affects derived indexing only. Default
// callers keep the existing IndexRecord implementation and canonical grants.
func indexOperatorMemoryPromotion(ctx context.Context, service *MemoryService, record model.MemoryRecordV2) error {
	if index, ok := ctx.Value(operatorMemoryDerivedIndexKey{}).(func(context.Context, model.MemoryRecordV2) error); ok && index != nil {
		return index(ctx, record)
	}
	return service.IndexRecord(ctx, record)
}

func operatorMemoryState(r model.MemoryRecordV2) string {
	return projectcontext.Digest("marshal.operator.memory-full-review.v1", r)
}

func (r *Runtime) OperatorMemoryReview(ctx context.Context, after string, limit int) (store.MemoryReviewPage, error) {
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		return store.MemoryReviewPage{}, err
	}
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return store.MemoryReviewPage{}, err
	}
	defer release()
	return r.store.ReadMemoryReviewPage(ctx, r.ProjectID(), principal.ID, after, limit)
}

// Live proposals remain untrusted retention requests. This metadata view never
// creates a canonical full-read capability or changes the original Decision.
func (r *Runtime) OperatorMemoryLiveReview(ctx context.Context, after string, limit int) (store.MemoryReviewPage, error) {
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		return store.MemoryReviewPage{}, err
	}
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return store.MemoryReviewPage{}, err
	}
	defer release()
	if len(after) > 256 || strings.ContainsRune(after, 0) || limit < 1 || limit > 32 {
		return store.MemoryReviewPage{}, model.ErrInvalid
	}
	page := store.MemoryReviewPage{Entries: []store.MemoryReviewEntry{}}
	for _, rec := range r.liveContinuationCandidates() {
		if rec.ID <= after || rec.ProjectID != r.ProjectID() || rec.ACLScope != "" && rec.ACLScope != principal.ID {
			continue
		}
		allowed := rec.Scope == string(model.ScopeProject) && rec.ScopeID == r.ProjectID() || (rec.Scope == string(model.ScopeAgent) || rec.Scope == string(model.ScopeOperatorPrivate)) && rec.ScopeID == principal.ID || rec.Scope == string(model.ScopeSession) && rec.ScopeID != ""
		if !allowed && model.MemoryScopeKind(rec.Scope) == model.ScopeTask {
			var taskErr error
			allowed, taskErr = r.store.MemoryReviewTaskScope(ctx, r.ProjectID(), rec.ScopeID)
			if taskErr != nil {
				return store.MemoryReviewPage{}, taskErr
			}
		}
		if !allowed {
			continue
		}
		if len(page.Entries) == limit {
			page.NextID = page.Entries[len(page.Entries)-1].ID
			break
		}
		page.Entries = append(page.Entries, store.MemoryReviewEntry{ID: rec.ID, Title: rec.Title, Lifecycle: string(rec.Lifecycle), Authority: string(rec.Authority), Scope: rec.Scope, ScopeID: rec.ScopeID, Revision: rec.Revision})
	}
	return page, nil
}

func (r *Runtime) BeginOperatorMemoryRead(ctx context.Context, id string) (*OperatorMemoryRead, error) {
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		return nil, err
	}
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return nil, err
	}
	defer release()
	if id == "" || len(id) > 256 || strings.ContainsRune(id, 0) {
		return nil, model.ErrInvalid
	}
	record, err := r.store.ReadOperatorReviewMemory(ctx, r.ProjectID(), principal.ID, id)
	if err != nil {
		return nil, err
	}
	if !utf8.ValidString(record.Body) || len(record.Body) > operatorMemoryReadMaximumBytes || record.ContentDigest != record.CanonicalDigest() {
		return nil, projectcontext.Changed("memory_review_extreme.go:144")
	}
	return &OperatorMemoryRead{runtime: r, actor: principal.ID, id: record.ID, revision: record.Revision, digest: record.ContentDigest, state: operatorMemoryState(record), size: len(record.Body), expires: time.Now().UTC().Add(5 * time.Minute)}, nil
}

// The trusted display guard examines the whole current authorized body before
// any slice is delivered. Withheld bytes never count toward complete review.
func (r *Runtime) ReadOperatorMemoryPage(ctx context.Context, token *OperatorMemoryRead, offset int, display func(string) string) (OperatorMemoryPage, error) {
	zero := OperatorMemoryPage{}
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		return zero, err
	}
	if token == nil || token.runtime != r || token.actor != principal.ID {
		return zero, projectcontext.Changed("memory_review_extreme.go:158")
	}
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return zero, err
	}
	defer release()
	token.mu.Lock()
	defer token.mu.Unlock()
	if display == nil || token.consumed || token.finished || !time.Now().UTC().Before(token.expires) || offset != token.next || offset < 0 || offset > token.size {
		return zero, projectcontext.Changed("memory_review_extreme.go:168")
	}
	record, err := r.store.ReadOperatorReviewMemory(ctx, r.ProjectID(), principal.ID, token.id)
	if err != nil || operatorMemoryState(record) != token.state || record.Revision != token.revision || record.ContentDigest != token.digest {
		token.consumed = true
		if err != nil {
			return zero, err
		}
		return zero, projectcontext.Changed("memory_review_extreme.go:176")
	}
	if display(record.Body) != record.Body {
		token.consumed, token.complete = true, false
		return OperatorMemoryPage{ID: record.ID, Title: record.Title, Lifecycle: string(record.Lifecycle), Authority: string(record.Authority), Confidence: string(record.Confidence), SourceKind: record.Source.Kind, Revision: record.Revision, BodyBytes: token.size, EvidenceCount: len(record.EvidenceIDs), Withheld: true}, nil
	}
	end := offset + OperatorMemoryPageBytes
	if end > len(record.Body) {
		end = len(record.Body)
	}
	for end < len(record.Body) && !utf8.RuneStart(record.Body[end]) {
		end--
	}
	token.next, token.complete = end, end == token.size
	token.finished = token.complete
	return OperatorMemoryPage{ID: record.ID, Title: record.Title, Body: record.Body[offset:end], Lifecycle: string(record.Lifecycle), Authority: string(record.Authority), Confidence: string(record.Confidence), SourceKind: record.Source.Kind, Revision: record.Revision, Offset: offset, NextOffset: end, BodyBytes: token.size, EvidenceCount: len(record.EvidenceIDs), FullRead: token.complete}, nil
}

// Matching is also called inside the existing Store mutator/CAS. There are no
// connection, runtime lock or source operations inside that transaction.
func checkOperatorMemoryPromotionRead(ctx context.Context, service *MemoryService, principal authz.Principal, record model.MemoryRecordV2) error {
	token, ok := ctx.Value(operatorMemoryPromotionKey{}).(*OperatorMemoryRead)
	if !ok {
		return nil // Existing supported non-review callers retain their contract.
	}
	if token == nil || token.runtime == nil || token.runtime.memoryService != service || principal.ID != token.actor || !token.complete || token.persisted || !time.Now().UTC().Before(token.expires) || record.ID != token.id || record.ProjectID != token.runtime.ProjectID() || record.Revision != token.revision || record.ContentDigest != token.digest || operatorMemoryState(record) != token.state || record.ACLScope != "" && record.ACLScope != principal.ID {
		return projectcontext.Changed("memory_review_extreme.go:202")
	}
	return nil
}

// Called only after the existing canonical UpdateMemory transaction succeeds.
// A conflicting foreign write cannot be mistaken for this promotion's commit.
func noteOperatorMemoryPromotionPersisted(ctx context.Context, service *MemoryService, principal authz.Principal, record model.MemoryRecordV2) {
	token, ok := ctx.Value(operatorMemoryPromotionKey{}).(*OperatorMemoryRead)
	if !ok || token == nil || token.runtime == nil || token.runtime.memoryService != service || token.actor != principal.ID || record.ID != token.id || record.Revision != token.revision+1 || record.Lifecycle != model.MemoryDurable || record.Authority != model.AuthorityOperator {
		return
	}
	token.persisted, token.promotedState, token.promotedRevision = true, operatorMemoryState(record), record.Revision
}

func (r *Runtime) CommandPromoteReviewedMemory(ctx context.Context, token *OperatorMemoryRead, rationale string) (OperatorMemoryPromotion, error) {
	zero := OperatorMemoryPromotion{}
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		return zero, err
	}
	if token == nil || token.runtime != r || token.actor != principal.ID {
		return zero, projectcontext.Changed("memory_review_extreme.go:224")
	}
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return zero, err
	}
	defer release()
	token.mu.Lock()
	defer token.mu.Unlock()
	rationale = strings.TrimSpace(rationale)
	if rationale == "" || len(rationale) > 2048 || !utf8.ValidString(rationale) || strings.ContainsRune(rationale, 0) {
		return zero, model.ErrInvalid
	}
	if !token.complete || !time.Now().UTC().Before(token.expires) {
		return zero, projectcontext.ErrReviewPending
	}
	record, err := r.store.ReadOperatorReviewMemory(ctx, r.ProjectID(), principal.ID, token.id)
	if err != nil {
		return zero, err
	}
	if err = r.memoryService.authorizer.Authorize(ctx, principal, authz.ActionMemoryPromote, record.ScopeID, model.MemoryDurable); err != nil {
		return zero, err
	}
	if model.MemoryScopeKind(record.Scope) == model.ScopeTask {
		if err = r.memoryService.authorizeTaskScope(ctx, principal, authz.ActionMemoryPromote, record.ScopeID); err != nil {
			return zero, err
		}
	}
	if token.consumed {
		// Retrying an index failure repairs only the unchanged derived view;
		// it never repeats a canonical promotion or changes its reason.
		if !token.persisted || token.indexed || token.rationale != rationale || record.Revision != token.promotedRevision || operatorMemoryState(record) != token.promotedState {
			return zero, projectcontext.Changed("memory_review_extreme.go:256")
		}
		result := OperatorMemoryPromotion{ID: record.ID, Revision: record.Revision, Persisted: true}
		result.Indexed = indexOperatorMemoryPromotion(ctx, r.memoryService, record) == nil
		token.indexed = result.Indexed
		return result, nil
	}
	token.consumed = true
	if record.Revision != token.revision || operatorMemoryState(record) != token.state || record.ContentDigest != token.digest {
		return zero, projectcontext.Changed("memory_review_extreme.go:265")
	}
	token.rationale = rationale
	mutation := store.WithMemoryMutationFullSnapshot(context.WithValue(ctx, operatorMemoryPromotionKey{}, token))
	_, promoteErr := r.memoryService.Promote(mutation, principal, PromoteRequest{ProjectID: r.ProjectID(), MemoryID: record.ID, ScopeID: record.ScopeID, Rationale: rationale})
	current, readErr := r.store.ReadOperatorReviewMemory(ctx, r.ProjectID(), principal.ID, record.ID)
	if !token.persisted || readErr != nil || current.Revision != token.promotedRevision || operatorMemoryState(current) != token.promotedState {
		known := zero
		if token.persisted {
			known = OperatorMemoryPromotion{ID: token.id, Revision: token.promotedRevision, Persisted: true}
		}
		if promoteErr != nil {
			return known, promoteErr
		}
		return known, projectcontext.Changed("memory_review_extreme.go:279")
	}
	token.indexed = promoteErr == nil
	return OperatorMemoryPromotion{ID: current.ID, Revision: current.Revision, Persisted: true, Indexed: promoteErr == nil}, nil
}

// liveContinuationCandidates is the in-process live-launch candidate set the
// operator reviews here. It is deliberately not the durable candidate listing
// (ContinuationCandidates): live review discloses only what this launch holds.
func (r *Runtime) liveContinuationCandidates() []model.MemoryRecordV2 {
	r.permissionMu.Lock()
	defer r.permissionMu.Unlock()
	out := make([]model.MemoryRecordV2, 0, len(r.continuationCandidates))
	for _, rec := range r.continuationCandidates {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// OperatorMemoryRetained reports whether the memory already has a canonical
// row, so a merged review does not list the same candidate twice.
func (r *Runtime) OperatorMemoryRetained(ctx context.Context, id string) bool {
	_, err := r.store.GetMemoryV2(ctx, r.ProjectID(), id)
	return err == nil
}
