//go:build governed

package app

import (
	"github.com/Zen1th53/marshal/internal/execution"
	"testing"
)

func assertPrivateLegacyMockExecutionRefused(t *testing.T, r *Runtime, before, after *execution.ExecutionRun, err error, calls int64) bool {
	return false
}
