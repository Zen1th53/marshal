//go:build governed

package store

import (
	"context"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// verifyArchitecture has nothing to read in the default edition: the
// architecture compatibility review is an Extreme record.
func (s *Store) verifyArchitecture(context.Context, string, string, marshal.Run) (*VerifyArchitecture, error) {
	return nil, nil
}
