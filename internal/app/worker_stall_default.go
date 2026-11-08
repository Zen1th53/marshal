//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func withWorkerStall(ctx context.Context, _ marshal.Settings) context.Context { return ctx }
func configureWorkerStall(context.Context, *adapter.Command)                  {}
