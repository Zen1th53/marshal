package tui

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/cloud"
)

// requestPresentation is what the durable request read becomes on screen. It
// must never fabricate a server identity or report an in-flight attempt as
// settled.
func TestEntitlementRequestReaderAdaptsFPresentationWithoutFabricatingServerIdentity(t *testing.T) {
	s := cloud.NewRequestStore(t.TempDir())
	key := cloud.RequestKey{Endpoint: "http://127.0.0.1:18743", InstallationID: "installation", SessionID: "cloud-session"}
	ctx := context.Background()
	read := func() (EntitlementRequestObservation, error) {
		o, found, err := s.Read(ctx, key)
		return requestPresentation(o, found), err
	}
	missing, err := read()
	if err != nil || missing.Recorded || missing.Error == "" {
		t.Fatalf("missing: %+v %v", missing, err)
	}
	attempt, err := s.Begin(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := read()
	if err != nil || !pending.Recorded || pending.State != "UNKNOWN" || pending.HasServerID || pending.ServerRequestID != "" {
		t.Fatalf("inflight: %+v %v", pending, err)
	}
	if _, err = s.Finish(ctx, attempt, cloud.EntitlementResponse{Status: "active"}, nil); err != nil {
		t.Fatal(err)
	}
	observed, err := read()
	if err != nil || observed.State != "active" || observed.HasServerID || observed.LocalAttemptID != attempt.LocalAttemptID || observed.SessionID != key.SessionID {
		t.Fatalf("presentation: %+v %v", observed, err)
	}
}
