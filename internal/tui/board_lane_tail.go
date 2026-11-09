package tui

import "context"

type laneTailState struct{}

func (w *Workspace) startLaneTail(ctx context.Context)  {}
func (w *Workspace) laneTailFor(paneID string) []string { return nil }
func (w *Workspace) laneTailWant() int                  { return 0 }
