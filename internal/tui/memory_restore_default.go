//go:build governed

package tui

import "context"

func buildRecoverySnapshot(context.Context, *ControlSource, *ControlSnapshot) {}
func (v *NavView) buildActionSupplement(*Node, int) []string                  { return nil }
