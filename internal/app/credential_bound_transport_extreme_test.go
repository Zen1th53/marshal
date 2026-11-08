//go:build !governed

package app

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

// The source fixture supplies no provider call. It exercises the actual
// Process05 transport pause/resume, credential Decision and egress publication.
func TestBoundCredentialProcess05TransportConsumesExactTask(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-process05-credential")
	r, task, pkg, canonical, agent := transportFixtureForProvider(t, false, "codex")
	// Extreme applies the exact task credential itself: nothing is queued.
	r.SetPermissionSink(func(req permission.Request) { t.Errorf("credential request queued: %+v", req) })
	task, ctx, _ := fixtureTransportApproval(t, r, task, pkg, canonical, agent, true)
	events, err := r.Store().ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	grants := 0
	for _, e := range events {
		if e.Type != "PERMISSION_DECIDED" || e.Data["kind"] != "credential-bound" {
			continue
		}
		scope, _ := e.Data["scope"].(string)
		if e.Data["object"] != "codex" || e.Data["allow"] != true || e.Data["decided_by"] != credentialRuntimeDecider || !strings.HasPrefix(e.Data["approval_binding"].(string), "CRED-BOUND-") || !strings.Contains(scope, task.RunID) || !strings.Contains(scope, task.TaskID) || !strings.Contains(scope, canonical.ID) || e.Data["bound_scope"] == "" {
			t.Fatalf("credential evidence escaped canonical task: %+v", e.Data)
		}
		grants++
	}
	if grants == 0 || r.HasCredentialGrant(ctx, "codex") {
		t.Fatal("task credential evidence missing or became standing grant")
	}
	socket, cleanup, err := r.startProviderEgress(ctx, "RUN-bounded-process05-egress", task.RunID, "codex", task.AssignedModel, canonical, agent, task.RunID, model.RoleDeveloper)
	if err != nil {
		t.Fatal(err)
	}
	lease, hasBound := r.boundCredentialForContext(ctx, "codex")
	r.egressMu.Lock()
	proxyCount := len(r.egressRuns)
	r.egressMu.Unlock()
	if socket != "" || proxyCount != 0 || !hasBound || lease == nil || lease.egressID != "RUN-bounded-process05-egress" || lease.socket != "" || !r.boundCredentialApproved(ctx, lease, lease.egressID, "") {
		t.Fatal("Process05 host transport lost its exact bounded credential or published a proxy")
	}
	if _, _, err := r.startProviderEgress(ctx, "RUN-bounded-process05-replay", task.RunID, "codex", task.AssignedModel, canonical, agent, task.RunID, model.RoleDeveloper); err == nil {
		t.Fatal("transport or credential reused for another egress incarnation")
	}
	cleanup()
	if r.boundCredentialApproved(ctx, lease, "RUN-bounded-process05-egress", "") {
		t.Fatal("host invocation retained its bounded credential after cleanup")
	}
}
