//go:build governed

package app

import "context"

// Community has no project contract waiting for the owner: the review is
// always the model-drafted one.
func (r *Runtime) StartArchitectureReviewFromContract(context.Context, string, string, string, ArchitectureReviewHooks) (*ArchitectureReviewSession, bool, error) {
	return nil, false, nil
}
