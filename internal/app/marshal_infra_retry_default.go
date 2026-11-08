//go:build governed

package app

import (
	"context"
	"errors"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The public edition keeps its rework semantics: a failed worker stops the
// dispatch with its error, and the returns it counts are unchanged.
func infraRetryApplies(marshal.Run, marshal.Task) bool { return false }

func (s *MarshalService) returnInfraFailure(context.Context, string, marshal.Run, int64, int, string) error {
	return errors.New("infrastructure retry is not available in this edition")
}

func resetInfraRetries(*marshal.Task) {}
