package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
)

type SlotPromotionReview struct {
	TaskID   string
	SlotType working.SlotType
	SourceID string
	Revision int64
	Digest   string
}

func (r *Runtime) memoryReviewPrincipal(ctx context.Context) (authz.Principal, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != r.ProjectIdentity() {
		return authz.Principal{}, authz.ErrDenied
	}
	return authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}, nil
}
func (r *Runtime) ReviewMemoryRecord(ctx context.Context, id string) (model.MemoryRecordV2, error) {
	p, err := r.memoryReviewPrincipal(ctx)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return r.memoryService.Get(ctx, p, r.ProjectID(), id)
}
func (r *Runtime) ReviewMemorySlot(ctx context.Context, task string, slot working.SlotType) (SlotPromotionReview, error) {
	var review SlotPromotionReview
	p, err := r.memoryReviewPrincipal(ctx)
	if err != nil {
		return review, err
	}
	slots, err := r.memoryService.ListTaskSlots(ctx, p, r.ProjectID(), task)
	if err != nil {
		return review, err
	}
	found := false
	for _, s := range slots {
		if s.Type == slot {
			found = true
			break
		}
	}
	if !found {
		return review, model.ErrNotFound
	}
	rec, err := r.store.GetMemoryV2(ctx, r.ProjectID(), taskSlotID(r.ProjectID(), task, string(slot)))
	if err != nil {
		return review, err
	}
	return SlotPromotionReview{TaskID: task, SlotType: slot, SourceID: rec.ID, Revision: rec.Revision, Digest: rec.ContentDigest}, nil
}
func (r *Runtime) CommandPromoteMemorySlot(ctx context.Context, e CommandEnvelope, review SlotPromotionReview, kind model.MemoryKind, title string) (model.MemoryRecordV2, error) {
	if review.SourceID != e.TargetID || review.Revision != e.ExpectedVersion || review.Digest == "" || !kind.IsValid() || title == "" {
		return model.MemoryRecordV2{}, model.ErrInvalid
	}
	ctx, p, command, err := r.memoryCommand(ctx, e, "memory.slot-promote", struct {
		Review SlotPromotionReview
		Kind   model.MemoryKind
		Title  string
	}{review, kind, title})
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if _, found, err := r.store.MemoryCommandResult(ctx, command); err != nil {
		return model.MemoryRecordV2{}, err
	} else if found {
		// Exact source revision plus canonical provenance identifies the result;
		// never create a replacement candidate on replay.
		records, err := r.memoryService.ListRecent(ctx, p, r.ProjectID(), 128)
		if err != nil {
			return model.MemoryRecordV2{}, err
		}
		for _, rec := range records {
			if rec.Scope == "task" && rec.ScopeID == review.TaskID && rec.Title == title && rec.ExtMeta["source_slot_type"] == string(review.SlotType) && int64(numericMemoryRevision(rec.ExtMeta["slot_revision"])) == review.Revision {
				return rec, nil
			}
		}
		return model.MemoryRecordV2{}, model.ErrNotFound
	}
	return r.memoryService.PromoteTaskSlot(ctx, p, r.ProjectID(), review.TaskID, review.SlotType, kind, title, review)
}
func numericMemoryRevision(value any) int { v, _ := numericMeta(value); return int(v) }
