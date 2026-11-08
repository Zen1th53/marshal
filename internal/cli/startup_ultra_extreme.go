//go:build !governed

package cli

import (
	"context"

	"github.com/Zen1th53/marshal/internal/cloud"
)

func startupUltraEntitled() bool {
	return cloud.Authorize(context.Background(), cloud.Config{}, "", Version).Gate.Entitled()
}
