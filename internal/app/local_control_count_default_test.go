//go:build governed

package app

// The Ctrl+N menu commands add one scoped grant each.
var expectedLocalControlGrantCount = 34 + len(memoryMenuLocalControlActions) + len(menuRestLocalControlActions)
