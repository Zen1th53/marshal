package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// architectureHandIn returns a hand-in whose code disagrees with the
// accepted architecture (see architectureHandInEdition).
func (s *MarshalService) architectureHandIn(ctx context.Context, t marshal.Task, h marshal.HandIn) error {
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return nil
	}
	return s.architectureHandInEdition(ctx, t, h)
}
