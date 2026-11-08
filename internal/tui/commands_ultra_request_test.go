package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/model"
)

// trackedRequestAuthority is a ControlAuthority that records whether the
// persisted request service was used, so a test can prove /ultra request no
// longer calls the untracked client directly.
type trackedRequestAuthority struct {
	fakeAuthority
	target      app.EntitlementRequestTarget
	state       string
	submitCalls int
	submitted   app.CommandEnvelope
	row         cloud.RequestObservation
}

func (a *trackedRequestAuthority) EntitlementRequestTarget(context.Context) (app.EntitlementRequestTarget, error) {
	return a.target, nil
}
func (a *trackedRequestAuthority) PersistedEntitlementRequest(context.Context) (cloud.RequestObservation, bool, error) {
	if a.row.LocalAttemptID == "" {
		return cloud.RequestObservation{}, false, nil
	}
	return a.row, true, nil
}
func (a *trackedRequestAuthority) SubmitEntitlementRequest(_ context.Context, e app.CommandEnvelope, target app.EntitlementRequestTarget) (cloud.RequestObservation, error) {
	a.submitCalls++
	a.submitted = e
	a.row = cloud.RequestObservation{Key: target.Key, Generation: e.ExpectedVersion + 2, LocalAttemptID: "local-attempt", State: a.state, AttemptedAt: time.Now().UTC()}
	return a.row, nil
}

func TestUltraRequestCommandRoutesThroughTrackedService(t *testing.T) {
	key := cloud.RequestKey{Endpoint: "http://127.0.0.1:18743", InstallationID: "installation", SessionID: "cloud-session"}
	for _, tc := range []struct {
		state   string
		want    string
		notWant string
	}{
		{"pending", "auto-approved by the service", ""},
		{"active", "already entitled", ""},
		{"unknown", "answer is unknown", "not sent"},
		{"sending", "answer is unknown", "not sent"},
		{"refused", "refused", ""},
	} {
		t.Run(tc.state, func(t *testing.T) {
			a := &trackedRequestAuthority{
				target: app.EntitlementRequestTarget{Key: key, WorkspaceSessionID: "s", ExpectedGeneration: 0},
				state:  tc.state,
			}
			ws := NewWorkspace(nil, "p", "s")
			ws.AttachControlSource(&ControlSource{Authority: a, SessionID: "s", ProjectID: "p"})
			out, err := ws.ExecuteCommand(context.Background(), "/ultra request")
			if err != nil {
				t.Fatalf("request command: %v", err)
			}
			if a.submitCalls != 1 {
				t.Fatalf("request did not route through the tracked service (submitCalls=%d)", a.submitCalls)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("output %q missing %q", out, tc.want)
			}
			if tc.notWant != "" && strings.Contains(out, tc.notWant) {
				t.Fatalf("output %q should not claim %q", out, tc.notWant)
			}
			if a.submitted.TargetID != model.SessionRecoveryDigest(key) {
				t.Fatalf("envelope TargetID %q does not bind the reviewed key", a.submitted.TargetID)
			}
			if a.submitted.ProjectID != "p" || a.submitted.SessionID != "s" {
				t.Fatalf("envelope project/session mismatched: %+v", a.submitted)
			}
		})
	}
}

// TestUltraRequestCommandFallsBackWhenServiceUnavailable confirms that a
// workspace with no runtime still answers "not configured" through the direct
// client path rather than panicking on a missing tracked service.
func TestUltraRequestCommandFallsBackWhenServiceUnavailable(t *testing.T) {
	ws := NewWorkspace(nil, "p", "s")
	out, err := ws.ExecuteCommand(context.Background(), "/ultra request")
	if err != nil {
		t.Fatalf("request command: %v", err)
	}
	if !strings.Contains(out, "not configured") {
		t.Fatalf("unconfigured request = %q", out)
	}
}
