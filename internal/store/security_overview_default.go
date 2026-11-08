//go:build governed

package store

import "context"

// securityUnconfined has nothing to read in the default edition: a lane
// without enforceable confinement is refused, never run on the host.
func (s *Store) securityUnconfined(context.Context) (*SecurityUnconfined, error) {
	return nil, nil
}
