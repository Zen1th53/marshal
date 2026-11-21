//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// bindArchitectureAcceptance makes a browser review built from a project
// contract read as accepted only when the store's accepted project contract
// is that contract (or a later revision of it), the same record
// marshal_project_contract_context qualifies on. The browser's own approved
// flag then only says the owner's part is done (OwnerAccepted). A review
// without a source contract keeps the review's own status.
func (r *Runtime) bindArchitectureAcceptance(ctx context.Context, state []byte, status *ArchitectureStatus) error {
	if r == nil || r.store == nil || status == nil {
		return nil
	}
	var bound struct {
		Architecture struct {
			Source *struct {
				ID       string `json:"id"`
				Revision int64  `json:"revision"`
				Digest   string `json:"digest"`
			} `json:"source_contract"`
		} `json:"architecture"`
	}
	if json.Unmarshal(state, &bound) != nil || bound.Architecture.Source == nil || bound.Architecture.Source.ID == "" {
		return nil
	}
	src := bound.Architecture.Source
	owner := status.Accepted
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	switch {
	case errors.Is(err, projectcontext.ErrReviewPending):
		status.Accepted = false
	case err != nil:
		return err
	default:
		same := accepted.Contract.ID == src.ID && accepted.Contract.Revision == src.Revision && accepted.Digest == src.Digest
		later := accepted.Contract.ID == src.ID && accepted.Contract.Revision > src.Revision
		status.Accepted = same || later
	}
	status.OwnerAccepted = owner && !status.Accepted
	return nil
}
