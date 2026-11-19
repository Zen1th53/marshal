//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Zen1th53/marshal/internal/archconform"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// architectureProgress reports only a recorded delivered result of this exact
// revision. Merely finding code on disk cannot establish that a run built it.
func (r *Runtime) architectureProgress(ctx context.Context) (json.RawMessage, error) {
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if errors.Is(err, projectcontext.ErrReviewPending) {
		return json.Marshal(map[string]any{"present": false})
	}
	if err != nil {
		return nil, err
	}
	history, err := r.store.ArchitectureCommitHistory(ctx, r.ProjectContractBinding(), 200)
	if err != nil {
		return nil, err
	}
	base, delivered := "", ""
	for _, binding := range history {
		if binding.Contract.Digest != accepted.Digest {
			continue
		}
		if binding.Event == "delivered" && delivered == "" {
			delivered = binding.Commit
		}
		if binding.Event == "accepted" {
			base = binding.Commit
		}
	}
	statuses := map[string]string{}
	var report *ArchitectureConformance
	if base != "" && delivered != "" {
		paths, err := gitMarshal(ctx, r.ProjectRoot(), "diff", "--name-only", "-z", base, delivered, "--")
		if err != nil {
			return nil, err
		}
		owners := archconform.NewMap(accepted.Contract)
		for _, path := range strings.Split(paths, "\x00") {
			if owner := owners.Owner(path); owner != "" {
				for _, part := range ancestorsOf(accepted.Contract, owner) {
					statuses[part] = "built"
				}
			}
		}
		checked, err := r.ArchitectureConformance(ctx, delivered)
		if err != nil {
			return nil, err
		}
		report = &checked
		for _, finding := range checked.Findings {
			statuses[finding.Part] = "differs from the architecture"
		}
		for _, id := range checked.UnimplementedParts {
			if statuses[id] != "" {
				statuses[id] = "differs from the architecture"
			}
		}
		// A partial check cannot justify a built label for the omitted code.
		if checked.Truncated || checked.MoreFindings > 0 {
			for id := range statuses {
				statuses[id] = "conformance check incomplete"
			}
		}
	}
	parts := []any{}
	for _, part := range accepted.Contract.Components {
		if part.Removed {
			continue
		}
		status := statuses[part.ID]
		if status == "" {
			status = "not built"
		}
		parts = append(parts, map[string]any{"id": part.ID, "status": status})
	}
	return json.Marshal(map[string]any{"present": true, "revision": accepted.Contract.Revision, "digest": accepted.Digest, "commit": delivered, "components": parts, "conformance": report})
}
