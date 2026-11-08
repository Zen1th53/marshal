//go:build governed

package store

import "context"

// architectureContract has nothing to read in the default edition: the
// project contract is an Extreme record.
func (s *Store) architectureContract(context.Context, string, *ArchitectureOverview) error {
	return nil
}
