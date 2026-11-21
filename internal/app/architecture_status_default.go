//go:build governed

package app

import "context"

// Community reviews carry no project contract: the review's own status is
// the acceptance.
func (r *Runtime) bindArchitectureAcceptance(context.Context, []byte, *ArchitectureStatus) error {
	return nil
}
