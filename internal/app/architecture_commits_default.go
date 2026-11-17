//go:build governed

package app

import "context"

// The governed edition keeps no architecture history.
func (r *Runtime) recordArchitectureCommitEdition(context.Context, string, string, string, string) {}
