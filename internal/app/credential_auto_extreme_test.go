//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

func runtimeCredentialDecisions(t *testing.T, r *Runtime, kind, provider string) []model.Event {
	t.Helper()
	events, err := r.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var out []model.Event
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" && e.Data["kind"] == kind && e.Data["object"] == provider && e.Data["decided_by"] == credentialRuntimeDecider {
			out = append(out, e)
		}
	}
	return out
}

// A provider probe's bounded credential is granted by the runtime with or
// without a decision surface; nothing reaches a popup or the Marshal, and the
// lease keeps its exact scope, expiry and one-invocation binding.
func TestExtremeBoundProbeCredentialAppliesAutomatically(t *testing.T) {
	for _, withSink := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-surface", true: "surface"}[withSink], func(t *testing.T) {
			r := openNetpolRuntime(t)
			queued := 0
			if withSink {
				r.SetPermissionSink(func(permission.Request) { queued++ })
			}
			scope := boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: "claude", Model: "claude-opus-5-5", Purpose: "probe", Probe: "PROBE-auto"}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			lease, err := r.waitBoundCredential(ctx, scope)
			if err != nil || queued != 0 {
				t.Fatalf("probe credential: err=%v queued=%d", err, queued)
			}
			if !r.boundCredentialApproved(ctx, lease, "", "") || r.HasCredentialGrant(ctx, "claude") {
				t.Fatal("probe lease not approved, or widened to a standing grant")
			}
			decisions := runtimeCredentialDecisions(t, r, "credential-bound", "claude")
			scopeJSON, _ := json.Marshal(scope)
			if len(decisions) != 1 || decisions[0].Data["approval_binding"] != lease.id || decisions[0].Data["bound_scope"] != string(scopeJSON) || decisions[0].Data["reason"] != credentialRuntimeReason || !strings.Contains(decisions[0].Data["scope"].(string), "PROBE-auto") {
				t.Fatalf("evidence: %+v", decisions)
			}
			if err := r.bindBoundCredentialEgress(ctx, lease, scope.Probe, "/tmp/auto.sock"); err != nil {
				t.Fatal(err)
			}
			if r.boundCredentialApproved(ctx, lease, "PROBE-other", "/tmp/auto.sock") {
				t.Fatal("lease escaped its one invocation")
			}
		})
	}
}

// The standing project grant is applied automatically, and a stray popup
// denial is superseded by the next automatic grant; only an explicit
// operator revoke keeps blocking.
func TestExtremeStandingCredentialAutoGrantAndPopupDenial(t *testing.T) {
	r := openNetpolRuntime(t)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if granted, err := r.waitCredentialGrant(ctx, "codex", time.Second); err != nil || !granted || !r.HasCredentialGrant(ctx, "codex") {
		t.Fatalf("no surface: granted=%v err=%v", granted, err)
	}
	popup := permission.Request{Kind: "credential", Object: "codex", Scope: "this project, until revoked", Who: "MARSHAL"}
	if err := r.CommandPermission(control.Context(ctx), popup, false, "operator popup"); err != nil {
		t.Fatal(err)
	}
	queued := 0
	r.SetPermissionSink(func(permission.Request) { queued++ })
	if granted, err := r.waitCredentialGrant(ctx, "codex", time.Second); err != nil || !granted || queued != 0 {
		t.Fatalf("popup denial stuck: granted=%v err=%v queued=%d", granted, err, queued)
	}
	if len(runtimeCredentialDecisions(t, r, "credential", "codex")) != 2 {
		t.Fatal("automatic grants not recorded")
	}
	// A popup-denied bounded lease does not stay terminal either.
	scope := boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: "codex", Model: "gpt-test", Purpose: "task", Run: "RUN-a", Task: "TASK-a", CanonicalTask: "TASK-c", Plan: "PLAN-a", PlanVersion: 1, Attempt: 1}
	denied, _, err := r.boundCredentialLease(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandPermission(control.Context(ctx), denied.request, false, "operator popup"); err != nil {
		t.Fatal(err)
	}
	if lease, err := r.waitBoundCredential(ctx, scope); err != nil || lease == denied || !r.boundCredentialApproved(ctx, lease, "", "") {
		t.Fatalf("popup-denied lease blocked the attempt: %v", err)
	}
	// The explicit operator revoke command still holds for both.
	if err := r.CommandPermission(control.Context(ctx), popup, false, "operator revoke"); err != nil {
		t.Fatal(err)
	}
	if granted, _ := r.waitCredentialGrant(ctx, "codex", time.Second); granted {
		t.Fatal("explicit operator revoke overridden")
	}
	next := scope
	next.Attempt++
	if _, err := r.waitBoundCredential(ctx, next); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("explicit revoke ignored by bounded grant: %v", err)
	}
}

func TestExtremeTaskCredentialLeaseOutlivesFiveMinutes(t *testing.T) {
	r := openNetpolRuntime(t)
	scope := boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: "claude", Model: "sonnet", Purpose: "task", Run: "RUN-long", Task: "TASK-long", CanonicalTask: "TASK-c", Plan: "PLAN-a", PlanVersion: 1, Attempt: 1}
	lease, _, err := r.boundCredentialLease(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if lease.expires.Sub(lease.created) < time.Hour {
		t.Fatalf("task lease window %v would close a running worker's proxy", lease.expires.Sub(lease.created))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	short, _, err := r.boundCredentialLease(ctx, func() boundCredentialScope { c := scope; c.Attempt = 2; return c }())
	if err != nil {
		t.Fatal(err)
	}
	if short.expires.Sub(short.created) > time.Minute {
		t.Fatal("invocation deadline did not shorten the lease")
	}
}
