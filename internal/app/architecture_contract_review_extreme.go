//go:build !governed

package app

import (
	"context"
	"errors"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// StartArchitectureReviewFromContract opens the browser review on the live
// launch's pending project contract, or the accepted project when none waits.
// The latter is read-only inspection and needs no active goal or provider.
// A contract replaces any older model-drafted view of an isolated goal.
func (r *Runtime) StartArchitectureReviewFromContract(ctx context.Context, provider, sessionID, launch string, hooks ArchitectureReviewHooks) (session *ArchitectureReviewSession, used bool, err error) {
	if r == nil || r.store == nil {
		return nil, false, nil
	}
	// A revision of an accepted contract waits for the owner exactly like a
	// first draft: ReadPendingProjectContract only returns a draft against the
	// active version, so the accepted one never shadows it.
	binding := r.ProjectContractBinding()
	accepted, acceptedErr := r.store.ReadAcceptedProjectContract(ctx, binding)
	if acceptedErr != nil && !errors.Is(acceptedErr, projectcontext.ErrReviewPending) {
		return nil, false, acceptedErr
	}
	var pendingContract projectcontext.Contract
	digest := ""
	pending, found, err := r.store.ReadPendingProjectContract(ctx, binding, launch)
	if err != nil {
		return nil, false, err
	}
	if found {
		pendingContract, digest = pending.Contract, pending.Digest
	} else if acceptedErr == nil {
		pendingContract, digest = accepted.Contract, accepted.Digest
		hooks.currentContract = true
	} else {
		return nil, false, nil
	}
	language, err := r.ApprovedMarshalLanguage()
	if err != nil {
		return nil, false, err
	}
	base := accepted.Contract
	if !found {
		base, err = r.previousProjectContract(ctx, pendingContract)
		if err != nil {
			return nil, true, err
		}
	}
	seed, err := contractReviewSeedFor(pendingContract, digest, language, base)
	if err != nil {
		return nil, true, err
	}
	hooks.fromContract = seed
	hooks.pendingContract = func(ctx context.Context) (*contractReviewSeed, error) {
		next, found, err := r.store.ReadPendingProjectContract(ctx, binding, launch)
		if err != nil || !found {
			return nil, err
		}
		current, readErr := r.store.ReadAcceptedProjectContract(ctx, binding)
		if readErr != nil && !errors.Is(readErr, projectcontext.ErrReviewPending) {
			return nil, readErr
		}
		return contractReviewSeedFor(next.Contract, next.Digest, language, current.Contract)
	}
	session, err = r.StartArchitectureReviewForSession(ctx, provider, "", sessionID, hooks)
	return session, true, err
}

// AcceptedArchitecture is the accepted project contract's revision and
// digest, "" when none is accepted. It reads only.
func (r *Runtime) AcceptedArchitecture(ctx context.Context) (revision int64, digest string) {
	if r == nil || r.store == nil {
		return 0, ""
	}
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		return 0, ""
	}
	return accepted.Contract.Revision, accepted.Digest
}

// StartAcceptedArchitectureReview opens the accepted project contract on its
// own read-only review: it never opens a pending revision, so no Accept can
// be reached from it. want is the digest the caller was shown; a different
// accepted contract now is refused.
func (r *Runtime) StartAcceptedArchitectureReview(ctx context.Context, provider, sessionID, want string, hooks ArchitectureReviewHooks) (*ArchitectureReviewSession, error) {
	if r == nil || r.store == nil {
		return nil, model.ErrUnavailable
	}
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		return nil, err
	}
	if want != "" && accepted.Digest != want {
		return nil, errors.New("The accepted architecture changed; look again.")
	}
	language, err := r.ApprovedMarshalLanguage()
	if err != nil {
		return nil, err
	}
	base, err := r.previousProjectContract(ctx, accepted.Contract)
	if err != nil {
		return nil, err
	}
	seed, err := contractReviewSeedFor(accepted.Contract, accepted.Digest, language, base)
	if err != nil {
		return nil, err
	}
	hooks.currentContract, hooks.fromContract = true, seed
	return r.StartArchitectureReviewForSession(ctx, provider, "", sessionID, hooks)
}
