package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cloud"
)

type requestMenuAuthority struct {
	fakeAuthority
	target   app.EntitlementRequestTarget
	row      cloud.RequestObservation
	state    string
	serverID string
	noProof  bool
	command  app.CommandEnvelope
}

func (a *requestMenuAuthority) EntitlementRequestTarget(context.Context) (app.EntitlementRequestTarget, error) {
	return a.target, nil
}
func (a *requestMenuAuthority) PersistedEntitlementRequest(context.Context) (cloud.RequestObservation, bool, error) {
	if a.noProof {
		return cloud.RequestObservation{}, false, nil
	}
	return a.row, a.row.LocalAttemptID != "", nil
}
func (a *requestMenuAuthority) SubmitEntitlementRequest(_ context.Context, e app.CommandEnvelope, target app.EntitlementRequestTarget) (cloud.RequestObservation, error) {
	a.command = e
	a.row = cloud.RequestObservation{Key: target.Key, Generation: e.ExpectedVersion + 2, LocalAttemptID: "local-attempt", ServerRequestID: a.serverID, State: a.state, AttemptedAt: time.Now().UTC()}
	return a.row, nil
}

func TestEntitlementRequestMenuSeparatesTrackingFromLease(t *testing.T) {
	for _, state := range []string{"pending", "active", "unknown", "sending"} {
		t.Run(state, func(t *testing.T) {
			a := &requestMenuAuthority{target: app.EntitlementRequestTarget{Key: cloud.RequestKey{Endpoint: "http://127.0.0.1:18743", InstallationID: "installation", SessionID: "cloud-session"}, WorkspaceSessionID: "workspace", ExpectedGeneration: 4}, state: state}
			s := &ControlSource{Authority: a, ProjectID: "project", SessionID: "workspace"}
			b := ultraRequestActionBindings(s)["CTUI-0035"]
			target, err := b.Prepare(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			req := ActionRequest{ProjectID: s.ProjectID, SessionID: s.SessionID, Target: target, IdempotencyKey: "exact-request"}
			out, err := b.Execute(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if a.command.ExpectedVersion != 4 || a.command.TargetID != target.ID || a.command.IdempotencyKey != "exact-request" {
				t.Fatalf("request rebind: %+v", a.command)
			}
			if state == "pending" || state == "active" {
				if !out.Succeeded() || !strings.Contains(out.Detail, "server request ID UNKNOWN") || !strings.Contains(out.Detail, "grants no ULTRA lease") {
					t.Fatalf("observation inflated: %+v", out)
				}
			} else if out.Verdict != VerdictUnknown || out.Succeeded() {
				t.Fatalf("ambiguous result: %+v", out)
			}
		})
	}
}

func TestEntitlementRequestMenuRequiresDurableAttemptProof(t *testing.T) {
	a := &requestMenuAuthority{noProof: true, state: "pending", target: app.EntitlementRequestTarget{Key: cloud.RequestKey{Endpoint: "http://127.0.0.1:18743", InstallationID: "installation", SessionID: "cloud-session"}, WorkspaceSessionID: "workspace"}}
	s := &ControlSource{Authority: a, ProjectID: "project", SessionID: "workspace"}
	b := ultraRequestActionBindings(s)["CTUI-0035"]
	target, _ := b.Prepare(context.Background())
	out, err := b.Execute(context.Background(), ActionRequest{ProjectID: s.ProjectID, SessionID: s.SessionID, Target: target, IdempotencyKey: "request"})
	if err != nil || out.Succeeded() || out.Verdict != VerdictUnknown {
		t.Fatalf("missing proof: %+v %v", out, err)
	}
}

func TestEntitlementRequestReaderAdaptsFPresentationWithoutFabricatingServerIdentity(t *testing.T) {
	s := cloud.NewRequestStore(t.TempDir())
	key := cloud.RequestKey{Endpoint: "http://127.0.0.1:18743", InstallationID: "installation", SessionID: "cloud-session"}
	reader := StoredEntitlementRequestReader{Store: s, Key: key}
	ctx := context.Background()
	missing, err := reader.EntitlementRequest(ctx)
	if err != nil || missing.Recorded || missing.Error == "" {
		t.Fatalf("missing: %+v %v", missing, err)
	}
	attempt, err := s.Begin(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := reader.EntitlementRequest(ctx)
	if err != nil || !pending.Recorded || pending.State != "UNKNOWN" || pending.HasServerID || pending.ServerRequestID != "" {
		t.Fatalf("inflight: %+v %v", pending, err)
	}
	if _, err = s.Finish(ctx, attempt, cloud.EntitlementResponse{Status: "active"}, nil); err != nil {
		t.Fatal(err)
	}
	observed, err := reader.EntitlementRequest(ctx)
	if err != nil || observed.State != "active" || observed.HasServerID || observed.LocalAttemptID != attempt.LocalAttemptID || observed.SessionID != key.SessionID {
		t.Fatalf("presentation: %+v %v", observed, err)
	}
}
