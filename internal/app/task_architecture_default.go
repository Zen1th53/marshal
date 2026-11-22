//go:build governed

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func (s *MarshalService) taskArchitecture(context.Context, marshal.Task) (string, bool) {
	return "ARCHITECTURE FOR THIS TASK: accepted contract store unavailable in governed edition.\n", false
}
