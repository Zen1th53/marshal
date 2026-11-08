//go:build governed

package app

import (
	"context"
	"testing"
)

func architectureServerContext(t *testing.T, r *Runtime, cli *MarshalCLI) context.Context {
	t.Helper()
	return t.Context()
}
