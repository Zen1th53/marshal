//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func (r *Runtime) previousProjectContract(ctx context.Context, c projectcontext.Contract) (projectcontext.Contract, error) {
	if c.Revision == 1 {
		return projectcontext.Contract{}, nil
	}
	return r.store.ReadProjectContractRevision(ctx, r.ProjectContractBinding(), projectcontext.ContractRef{ID: c.ID, Revision: c.Revision - 1, Digest: c.SupersedesDigest})
}

func (r *Runtime) configureArchitectureHistory(config *architecturereview.Config) {
	config.Conformance = r.architectureProgress
	config.History = func(ctx context.Context, revision int64) (json.RawMessage, error) {
		list, err := r.store.ProjectContractRevisions(ctx, r.ProjectContractBinding())
		if err != nil {
			return nil, err
		}
		if revision == 0 {
			return json.Marshal(map[string]any{"revisions": list})
		}
		for _, entry := range list {
			if entry.Revision != revision {
				continue
			}
			base, err := r.previousProjectContract(ctx, entry.Contract)
			if err != nil {
				return nil, err
			}
			language, err := r.ApprovedMarshalLanguage()
			if err != nil {
				return nil, err
			}
			seed, err := contractReviewSeedFor(entry.Contract, entry.Digest, language, base)
			if err != nil {
				return nil, err
			}
			layouts, err := architecturereview.GenericLayouts(seed.Architecture)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"revision": entry.Revision, "base_revision": base.Revision, "date": entry.Date, "goal": entry.Goal, "run": entry.Run, "accepted_by": entry.AcceptedBy, "architecture": seed.Architecture, "ui": seed.UI, "layouts": layouts, "read_only": true})
		}
		return nil, fmt.Errorf("no accepted architecture revision %d in the available history", revision)
	}
}
