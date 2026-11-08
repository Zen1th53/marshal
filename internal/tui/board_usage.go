package tui

import "context"

type usageState struct{}

func (w *Workspace) startUsage(ctx context.Context)               {}
func (w *Workspace) usageSegment(width int) (wide, narrow string) { return "", "" }
