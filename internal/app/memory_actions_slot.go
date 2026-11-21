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
