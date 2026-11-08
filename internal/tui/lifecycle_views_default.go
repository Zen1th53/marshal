//go:build governed

package tui

func (w *Workspace) attachLifecycleSource(*StatusSource) {}

func buildLifecycleGapFields(string, Snapshot) []Field { return nil }
