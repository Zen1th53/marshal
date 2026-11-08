//go:build governed

package lifecycle

import (
	"github.com/Zen1th53/marshal/internal/app"
	"testing"
)

func assertPrivateUnboundLifecycle(t *testing.T, r *app.Runtime, a *Adapter, req Request, out Result, err error, calls int64) bool {
	return false
}
