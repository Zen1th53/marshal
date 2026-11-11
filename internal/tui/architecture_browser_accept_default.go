//go:build governed

package tui

import (
	"context"
	"encoding/json"

	"github.com/Zen1th53/marshal/internal/app"
)

// Community has no project-contract instruction, so the browser Accept needs
// no before-hook and is never blocked on a project summary.
func (w *Workspace) beforeBrowserAccept() func(context.Context, json.RawMessage) error { return nil }

func (w *Workspace) browserAcceptBlocked() func() string { return nil }

// startArchitectureReviewSession is always the model-drafted review here.
func (w *Workspace) startArchitectureReviewSession(ctx context.Context, rt *app.Runtime, provider, goal string) (*app.ArchitectureReviewSession, error) {
	return rt.StartArchitectureReviewForSession(ctx, provider, goal, w.sessionID, w.architectureReviewHooks())
}
