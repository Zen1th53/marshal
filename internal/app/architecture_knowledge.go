package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/architecturecodec"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// ArchitectureAcceptance returns only a receipt for the currently observed
// canonical review and full source snapshot. Historical approvals and a
// caller-supplied project role confer no authority here.
func (r *Runtime) ArchitectureAcceptance(ctx context.Context) (store.ArchitectureAcceptance, error) {
	if r == nil || r.store == nil {
		return store.ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	if architecturecodec.ModelArchitectureApprovalAllowed() {
		if accepted, err := r.store.MarshalArchitectureAcceptanceFor(ctx, r.ProjectIdentity()); err == nil {
			return accepted, nil
		}
	}
	observed, _, err := r.store.ObserveRegisteredArchitectureOwner(ctx)
	if err != nil {
		return store.ArchitectureAcceptance{}, err
	}
	accepted, err := r.store.ArchitectureAcceptanceFor(ctx, observed.Binding.ProjectID)
	if err != nil {
		return store.ArchitectureAcceptance{}, err
	}
	if accepted.ArchitectureTarget != observed.Binding.ArchitectureTarget ||
		accepted.MatchesReview(observed.Binding.ArchitectureID, observed.Binding.Revision, observed.Binding.Digest, observed.Binding.LayoutDigest, observed.Binding.UIDigest) != nil ||
		accepted.SourceHead != observed.Binding.SourceHead || accepted.SourceTree != observed.Binding.SourceTree || accepted.SourceDirty != observed.Binding.SourceDirty || !architecturecodec.ArchitectureApprovalOriginAllowed(accepted) {
		return store.ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	return accepted, nil
}
