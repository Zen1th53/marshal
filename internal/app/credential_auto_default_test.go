//go:build governed

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
)

// Community keeps the operator decision: without a surface a probe credential
// is refused, with one it is queued, and nothing is granted by the runtime.
func TestDefaultCredentialsStillAwaitOperator(t *testing.T) {
	r := openNetpolRuntime(t)
	scope := boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: "claude", Model: "claude-opus-5-5", Purpose: "probe", Probe: "PROBE-default"}
	if _, err := r.waitBoundCredential(t.Context(), scope); err == nil || !strings.Contains(err.Error(), "no credential decision surface") {
		t.Fatalf("probe without surface: %v", err)
	}
	if _, err := r.waitCredentialGrant(t.Context(), "claude", time.Second); err == nil {
		t.Fatal("standing grant without operator surface")
	}
	queued := 0
	r.SetPermissionSink(func(req permission.Request) { queued++ })
	if granted, _ := r.waitCredentialGrant(t.Context(), "claude", 30*time.Millisecond); granted || queued != 1 {
		t.Fatalf("granted=%v queued=%d", granted, queued)
	}
	events, err := r.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" {
			t.Fatalf("runtime decided a Community credential: %+v", e.Data)
		}
	}
}
