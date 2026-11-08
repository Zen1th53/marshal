//go:build governed

package app

import (
	"context"
	"testing"
)

func requiredMarshalTestContext(t *testing.T, m *MarshalCLI) context.Context {
	t.Helper()
	return t.Context()
}
