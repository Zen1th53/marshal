//go:build governed

package tui

import "context"

// The Goal is the operator's in this edition: a model proposal never creates
// or confirms it, so no goal proposal parses and none is applied.
func (w *Workspace) applyMarshalGoalProposal(p marshalProposal) {
	if w.store != nil && p.occurrence != "" {
		_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "rejected")
	}
	w.RecordActivity("Ignored Marshal goal proposal: the Goal is set and confirmed by the operator.")
}
