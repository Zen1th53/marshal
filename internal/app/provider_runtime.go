package app

import (
	"context"
	"sort"

	"github.com/Zen1th53/marshal/internal/project"
)

// ProviderObservation is an evidence-bearing local provider discovery result.
// Discovery proves only executable presence. Version, model, authentication,
// quota and capacity remain unknown until their own governed probes run.
type ProviderObservation struct {
	Name   string
	Found  bool
	Path   string
	Probed bool
	Note   string
}

// ProviderObservations performs the same executable discovery used by the
// canonical CLI adapter surface. It never launches a provider process and
// therefore cannot accidentally turn a Status refresh into execution.
func (r *Runtime) ProviderObservations(ctx context.Context) ([]ProviderObservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names := map[string]struct{}{
		"claude": {}, "codex": {}, "gemini": {}, "opencode": {}, "antigravity": {},
	}
	if r != nil {
		for name := range r.adapters {
			names[name] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	rows := make([]ProviderObservation, 0, len(ordered))
	for _, name := range ordered {
		path, err := project.FindBinary(name)
		row := ProviderObservation{Name: name, Probed: true}
		if err == nil {
			row.Found, row.Path = true, path
			row.Note = "executable discovered; version qualification has not run"
		} else {
			row.Note = "provider executable was not found"
		}
		rows = append(rows, row)
	}
	return rows, nil
}
