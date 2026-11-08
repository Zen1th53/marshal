package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/permission"
)

func TestBoundCredentialOneInvocationNeverBecomesStandingGrant(t *testing.T) {
	requireOperatorCredentialDecisions(t)
	r := openNetpolRuntime(t)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope := boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: "codex", Model: "gpt-6.1-sol", Purpose: "probe", Probe: "PROBE-exact"}
	queued := make(chan permission.Request, 1)
	r.SetPermissionSink(func(req permission.Request) { queued <- req })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.waitBoundCredential(ctx, scope); done <- err }()
	req := <-queued
	if req.Kind != "credential-bound" || req.ApprovalBinding == "" || !strings.Contains(req.Scope, scope.Probe) || !strings.Contains(req.Scope, scope.Model) || req.Reason == "" {
		t.Fatalf("unreviewable bounded request: %+v", req)
	}
	if err := r.CommandPermission(ctx, req, true, "provider child imitation"); err == nil {
		t.Fatal("provider context approved a bounded credential")
	}
	for _, forged := range []permission.Request{{Kind: "credential-bound", Object: "codex", ApprovalBinding: "CRED-BOUND-fake"}, func() permission.Request { copy := req; copy.Scope = "this project, until revoked"; return copy }(), func() permission.Request { copy := req; copy.ProposalID = "mcp:forged"; return copy }(), func() permission.Request { copy := req; copy.ContinuationProvider = "codex"; return copy }()} {
		if err := r.CommandPermission(control.Context(ctx), forged, true, "synthetic"); err == nil {
			t.Fatal("synthetic bounded grant accepted")
		}
	}
	if err := r.CommandPermission(control.Context(ctx), req, true, "exact probe"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if r.HasCredentialGrant(ctx, "codex") {
		t.Fatal("bounded probe became a standing project grant")
	}
	r.SetPermissionSink(nil)
	if _, err := r.providerBroker(ctx, "codex", scope.Model); err == nil {
		t.Fatal("plain Runtime.Run path inherited bounded credential without context")
	}
	if _, err := r.providerBroker(withBoundCredentialScope(ctx, r, scope), "codex", "wrong-model"); err == nil {
		t.Fatal("different model inherited bounded credential")
	}
	lease := r.existingBoundCredential(scope)
	if lease == nil || !r.boundCredentialApproved(ctx, lease, "", "") {
		t.Fatal("exact probe grant missing")
	}
	if err := r.bindBoundCredentialEgress(ctx, lease, scope.Probe, "/tmp/exact-probe.sock"); err != nil {
		t.Fatal(err)
	}
	if !r.boundCredentialApproved(ctx, lease, scope.Probe, "/tmp/exact-probe.sock") || r.boundCredentialApproved(ctx, lease, scope.Probe, "/tmp/wrong.sock") || r.boundCredentialApproved(ctx, lease, "PROBE-other", "/tmp/exact-probe.sock") {
		t.Fatal("probe lease escaped its egress incarnation")
	}
	r.closeBoundCredentialEgress(lease, scope.Probe, "/tmp/exact-probe.sock")
	if r.boundCredentialApproved(ctx, lease, scope.Probe, "/tmp/exact-probe.sock") {
		t.Fatal("closed probe lease remained usable")
	}
	if err := r.CommandPermission(control.Context(ctx), req, true, "replay"); err == nil {
		t.Fatal("answered probe decision replayed")
	}
}

func TestBoundCredentialExpiredReviewNeedsFreshDecisionButDenialStaysTerminal(t *testing.T) {
	requireOperatorCredentialDecisions(t)
	r := openNetpolRuntime(t)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	base := boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: "codex", Model: "gpt-test", Purpose: "task", Run: "RUN-review", Task: "TASK-review", CanonicalTask: "TASK-canonical", Plan: "PLAN-review", PlanVersion: 1, Attempt: 1}
	unsafe := base
	unsafe.Model = "gpt-\u202Etest"
	if _, _, err := r.boundCredentialLease(t.Context(), unsafe); err == nil {
		t.Fatal("bidi model selector reached Decision display")
	}
	first, fresh, err := r.boundCredentialLease(t.Context(), base)
	if err != nil || !fresh {
		t.Fatal(first, fresh, err)
	}
	if err := r.CommandPermission(control.Context(t.Context()), first.request, true, "fixture bounded review"); err != nil {
		t.Fatal(err)
	}
	r.boundCredentials.mu.Lock()
	first.expires = time.Now().Add(-time.Second)
	r.boundCredentials.mu.Unlock()
	second, fresh, err := r.boundCredentialLease(t.Context(), base)
	if err != nil || !fresh || second.id == first.id {
		t.Fatalf("expired review reused old consent: %v %+v %+v", err, first, second)
	}
	if r.boundCredentialApproved(t.Context(), first, "", "") {
		t.Fatal("expired historical allow revived")
	}
	if err := r.CommandPermission(control.Context(t.Context()), second.request, false, "fixture denies retry"); err != nil {
		t.Fatal(err)
	}
	third, fresh, err := r.boundCredentialLease(t.Context(), base)
	if err != nil || fresh || third != second {
		t.Fatal("denied same task attempt silently re-prompted")
	}
	next := base
	next.Attempt++
	fourth, fresh, err := r.boundCredentialLease(t.Context(), next)
	if err != nil || !fresh || fourth.id == second.id {
		t.Fatal("new task attempt could not request separate consent")
	}
}

func TestBoundCredentialEgressAndSandboxUseExactLiveProbe(t *testing.T) {
	r := openNetpolRuntime(t)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope := boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: "codex", Model: "gpt-test", Purpose: "probe", Probe: "PROBE-socket"}
	ctx := withBoundCredentialScope(t.Context(), r, scope)
	lease, _, err := r.boundCredentialLease(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandPermission(control.Context(ctx), lease.request, true, "exact sandbox probe"); err != nil {
		t.Fatal(err)
	}
	broker, err := netpolicy.NewCredentialBroker("codex", "gpt-test", "synthetic-bounded-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.startRunEgress(ctx, "PROBE-wrong", "", "codex", "PROBE-wrong", "runtime", []string{"api.openai.com:443"}, broker); err == nil {
		t.Fatal("wrong probe reached egress")
	}
	socket, cleanup, err := r.startRunEgress(ctx, scope.Probe, "", "codex", scope.Probe, "runtime", []string{"api.openai.com:443"}, broker)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	r.egressMu.Lock()
	live := r.egressRuns[scope.Probe]
	r.egressMu.Unlock()
	if live == nil || live.boundCredential != lease || !r.credentialAllowedForEgress(ctx, live) {
		t.Fatal("live egress lost exact bound grant")
	}
	request := netpolicy.Request{Host: "api.openai.com", Port: 443, Protocol: netpolicy.ProtocolTCP}
	if decision, err := (&storedRunEgress{runtime: r, scope: live}).Evaluate(ctx, request); err != nil || !decision.Allowed {
		t.Fatalf("exact probe egress: %+v %v", decision, err)
	}
	if _, err := r.sandboxBroker(socket, t.TempDir()); err != nil {
		t.Fatalf("exact sandbox broker mount: %v", err)
	}
	if env, err := r.sandboxBroker("/tmp/foreign.sock", t.TempDir()); err != nil || len(env) != 0 {
		t.Fatalf("foreign socket inherited bound credential: %v %v", env, err)
	}
	if _, _, err := r.startRunEgress(ctx, "PROBE-replay", "", "codex", "PROBE-replay", "runtime", []string{"api.openai.com:443"}, broker); err == nil {
		t.Fatal("second invocation inherited probe grant")
	}
	r.boundCredentials.mu.Lock()
	lease.expires = time.Now().Add(-time.Second)
	r.boundCredentials.mu.Unlock()
	if r.credentialAllowedForEgress(ctx, live) {
		t.Fatal("expired probe retained broker access")
	}
	if decision, err := (&storedRunEgress{runtime: r, scope: live}).Evaluate(ctx, request); err != nil || decision.Allowed {
		t.Fatalf("expired probe egress: %+v %v", decision, err)
	}
	deadline := time.Now().Add(time.Second)
	for egressScopeLive(socket) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if egressScopeLive(socket) {
		t.Fatal("expired broker connection remained open")
	}
}

func TestBoundTaskCredentialEgressConsumesOneCanonicalAttempt(t *testing.T) {
	r := openNetpolRuntime(t)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope := boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: "claude", Model: "sonnet", Purpose: "task", Run: "RUN-parent", Task: "TASK-process05", CanonicalTask: "TASK-canonical", Plan: "PLAN-canonical", PlanVersion: 1, Attempt: 2}
	ctx := withBoundCredentialScope(t.Context(), r, scope)
	lease, _, err := r.boundCredentialLease(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandPermission(control.Context(ctx), lease.request, true, "exact task attempt"); err != nil {
		t.Fatal(err)
	}
	broker, err := netpolicy.NewCredentialBroker("claude", "sonnet", "synthetic-bounded-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.startRunEgress(ctx, "RUN-wrong", "RUN-other", "claude", scope.CanonicalTask, "worker", []string{"api.anthropic.com:443"}, broker); err == nil {
		t.Fatal("wrong parent run consumed task credential")
	}
	if _, _, err := r.startRunEgress(ctx, "RUN-wrong", scope.Run, "claude", "TASK-other", "worker", []string{"api.anthropic.com:443"}, broker); err == nil {
		t.Fatal("wrong canonical task consumed credential")
	}
	socket, cleanup, err := r.startRunEgress(ctx, "RUN-exact-egress", scope.Run, "claude", scope.CanonicalTask, "worker", []string{"api.anthropic.com:443"}, broker)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !r.boundCredentialApproved(ctx, lease, "RUN-exact-egress", socket) {
		t.Fatal("exact task egress not authorized")
	}
	if _, _, err := r.startRunEgress(ctx, "RUN-second-egress", scope.Run, "claude", scope.CanonicalTask, "worker", []string{"api.anthropic.com:443"}, broker); err == nil {
		t.Fatal("second egress invocation reused task credential")
	}
}

func TestBoundTaskCredentialExpiresAndBroadRevokeWins(t *testing.T) {
	requireOperatorCredentialDecisions(t)
	r := openNetpolRuntime(t)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope := boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: "claude", Model: "sonnet", Purpose: "task", Run: "RUN-exact", Task: "TASK-exact", CanonicalTask: "TASK-canonical", Plan: "PLAN-exact", PlanVersion: 2, Attempt: 1}
	queued := make(chan permission.Request, 1)
	r.SetPermissionSink(func(req permission.Request) { queued <- req })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.waitBoundCredential(ctx, scope); done <- err }()
	req := <-queued
	for _, part := range []string{scope.Run, scope.Task, scope.CanonicalTask, "attempt 1", scope.Plan, "v2"} {
		if !strings.Contains(req.Scope, part) {
			t.Fatalf("missing %q from exact task scope %q", part, req.Scope)
		}
	}
	if err := r.CommandPermission(control.Context(ctx), req, true, "exact task"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	lease := r.existingBoundCredential(scope)
	if lease == nil || !r.boundCredentialApproved(ctx, lease, "", "") {
		t.Fatal("task grant missing")
	}
	if r.existingBoundCredential(func() boundCredentialScope { copy := scope; copy.Attempt++; return copy }()) != nil {
		t.Fatal("another attempt inherited grant")
	}
	if err := r.CommandPermission(control.Context(ctx), permission.Request{Kind: "credential", Object: "claude"}, false, "broad revoke"); err != nil {
		t.Fatal(err)
	}
	if r.boundCredentialApproved(ctx, lease, "", "") {
		t.Fatal("broad revoke did not invalidate task grant")
	}
	// Expiry is independently enforced even if no new proxy request arrives.
	r.boundCredentials.mu.Lock()
	lease.expires = time.Now().Add(-time.Second)
	r.boundCredentials.mu.Unlock()
	if r.boundCredentialApproved(ctx, lease, "", "") {
		t.Fatal("expired task grant remained usable")
	}
}
