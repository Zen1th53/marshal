//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The governed edition has no project contract to check against.
func (s *MarshalService) architectureHandInEdition(context.Context, marshal.Task, marshal.HandIn) error {
	return nil
}
