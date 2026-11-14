//go:build !governed

package app

import (
	"context"
	"errors"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// StartArchitectureReviewFromContract opens the browser review on the live
// launch's pending project contract (a first draft, or a new revision of the
// accepted one), so the owner can read and Accept it without any Marshal
// model turn. It runs no model turn. used is false when this path does not
// apply (none is pending, or a model-drafted review exists):
// the caller then takes the model-drafted path.
func (r *Runtime) StartArchitectureReviewFromContract(ctx context.Context, provider, sessionID, launch string, hooks ArchitectureReviewHooks) (session *ArchitectureReviewSession, used bool, err error) {
	if r == nil || r.store == nil || launch == "" {
		return nil, false, nil
	}
	// A revision of an accepted contract waits for the owner exactly like a
	// first draft: ReadPendingProjectContract only returns a draft against the
	// active version, so the accepted one never shadows it.
	binding := r.ProjectContractBinding()
	if _, err := r.store.ReadAcceptedProjectContract(ctx, binding); err != nil && !errors.Is(err, projectcontext.ErrReviewPending) {
		return nil, false, err
	}
	pending, found, err := r.store.ReadPendingProjectContract(ctx, binding, launch)
	if err != nil || !found {
		return nil, false, err
	}
	directory, err := architectureReviewDirectory(r.layout.RuntimeDir)
	if err != nil {
		return nil, false, err
	}
	if modelDrafted, err := modelDraftedReviewExists(directory); err != nil || modelDrafted {
		return nil, false, err
	}
	language, err := r.ApprovedMarshalLanguage()
	if err != nil {
		return nil, false, err
	}
	seed, err := contractReviewSeedFor(pending.Contract, pending.Digest, language)
	if err != nil {
		return nil, true, err
	}
	hooks.fromContract = seed
	session, err = r.StartArchitectureReviewForSession(ctx, provider, "", sessionID, hooks)
	return session, true, err
}
