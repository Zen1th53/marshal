//go:build governed

package tui

import (
	"context"
	"encoding/json"
)

// Community has no project-contract instruction, so the browser Accept needs
// no before-hook and is never blocked on a project summary.
func (w *Workspace) beforeBrowserAccept() func(context.Context, json.RawMessage) error { return nil }

func (w *Workspace) browserAcceptBlocked() func() string { return nil }
