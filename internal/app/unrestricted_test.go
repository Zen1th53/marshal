package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

// The private edition never stops a governed run to ask about a destination:
// the proxy grants it before the first connection and records the decision.
// Community still refuses and queues an operator request.
func TestEditionEgressUndecidedDestination(t *testing.T) {
	r := openNetpolRuntime(t)
	alerts := 0
	r.SetEgressAlertSink(func(EgressAlert) error { alerts++; return nil })
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })}
	go func() { _ = server.Serve(upstream) }()
	defer server.Close()
	endpoint := upstream.Addr().String()
	socket, cleanup, err := r.startRunEgress(t.Context(), "RUN-open", "", "check", "TASK-open", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	want := http.StatusForbidden
	if openEgressEdition {
		want = http.StatusNoContent
	}
	for range 2 {
		if status := egressProxyGet(t, socket, endpoint); status != want {
			t.Fatalf("undecided destination: %d, want %d", status, want)
		}
	}
	history, err := r.store.EgressControlEvents(t.Context(), "RUN-open", 0)
	if err != nil {
		t.Fatal(err)
	}
	grants, notifications := 0, 0
	for _, e := range history {
		switch e.Type {
		case events.EventTypeNetworkEgressGranted:
			if e.Data["source"] == "provider default" {
				continue
			}
			grants++
			if e.Data["source"] != "extreme open egress" || e.Data["decided_by"] != "marshal-runtime" || e.Data["reason"] != "Marshal: network egress applies automatically" || e.Data["endpoint"] != endpoint || e.Data["scope_socket"] != socket {
				t.Fatalf("incomplete automatic egress evidence: %+v", e)
			}
		case events.EventTypeNetworkEgressNotification:
			notifications++
		}
	}
	if openEgressEdition && (grants != 1 || notifications != 0 || alerts != 0 || r.EgressRequestPending("RUN-open", endpoint)) {
		t.Fatalf("private edition asked: grants %d notifications %d alerts %d", grants, notifications, alerts)
	}
	if !openEgressEdition && (grants != 0 || notifications != 2 || alerts != 2 || !r.EgressRequestPending("RUN-open", endpoint)) {
		t.Fatalf("community changed: grants %d notifications %d alerts %d", grants, notifications, alerts)
	}
	if !openEgressEdition {
		return
	}
	// An explicit operator revocation still wins over the automatic grant.
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandEgress(control.Context(t.Context()), "RUN-open", "revoke", endpoint); err != nil {
		t.Fatal(err)
	}
	if status := egressProxyGet(t, socket, endpoint); status != http.StatusForbidden {
		t.Fatalf("automatic grant reopened a revoked destination: %d", status)
	}
	if _, automatic := r.AutomaticOpenEgressRequest(t.Context(), "RUN-open", endpoint); automatic {
		t.Fatal("revoked destination offered for automatic grant")
	}
}

// Undecided folders outside the project: the private edition reads them and
// records one runtime decision per folder; an explicit denial still wins.
// Community denies without evidence.
func TestEditionReadOfUndecidedFolder(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte("plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		data, err := r.ReadGranted(ctx, path)
		if unrestrictedEdition && (err != nil || string(data) != "plan") || !unrestrictedEdition && !errors.Is(err, authz.ErrDenied) {
			t.Fatalf("undecided read: %q %v", data, err)
		}
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	recorded := 0
	list, err := r.store.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list {
		if e.Type == "PERMISSION_DECIDED" && e.Data["kind"] == "read" {
			recorded++
			if e.Data["object"] != resolved || e.Data["allow"] != true || e.Data["decided_by"] != "marshal-runtime" || e.Data["source"] != "marshal" || e.Data["reason"] != "Marshal: reads outside the project apply automatically" {
				t.Fatalf("incomplete automatic read evidence: %+v", e)
			}
		}
	}
	if want := map[bool]int{true: 1, false: 0}[unrestrictedEdition]; recorded != want {
		t.Fatalf("read evidence %d, want %d", recorded, want)
	}
	if grants, err := r.OperatorReadGrants(ctx); err != nil || len(grants) != 0 {
		t.Fatalf("automatic read invented an operator grant: %+v %v", grants, err)
	}
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandPermission(control.Context(ctx), permission.Request{Kind: "read", Object: dir, Scope: "this project, until revoked, read-only", Who: "operator", Reason: "operator read decision"}, false, "operator command"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadGranted(ctx, path); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("explicit denial ignored: %v", err)
	}
}

// A developer's network for an R2/R3 task is refused only in Community.
func TestEditionHighRiskDeveloperNetwork(t *testing.T) {
	err := authorizeNetworkAccess(nil, "AGENT-dev", "SESSION-dev", "TASK-risk", model.RoleDeveloper, model.R3, true)
	if unrestrictedEdition && err != nil {
		t.Fatalf("private edition refused high-risk developer network: %v", err)
	}
	if !unrestrictedEdition && (!errors.Is(err, model.ErrPolicyDenied) || !strings.Contains(err.Error(), "high-risk")) {
		t.Fatalf("community high-risk network check changed: %v", err)
	}
	if err := authorizeNetworkAccess(nil, "AGENT-dev", "SESSION-dev", "TASK-risk", model.RoleDeveloper, model.R3, false); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("unrequired network allowed: %v", err)
	}
}

// Extreme never cuts a governed task launch (one model turn) by wall clock and
// never bounds fresh-session retries; Community keeps 30 minutes and 2.
func TestEditionWorkLimits(t *testing.T) {
	timeout, retries := buildTaskLaunchTimeout(), buildMaxFreshSessionRetries()
	if unrestrictedEdition {
		if timeout != 0 || retries != 0 {
			t.Fatalf("Marshal limits work: timeout=%v retries=%d", timeout, retries)
		}
		return
	}
	if timeout != 30*time.Minute || retries != 2 {
		t.Fatalf("Community limits changed: timeout=%v retries=%d", timeout, retries)
	}
}
