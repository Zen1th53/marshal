package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// Asking for an entitlement is not a way to obtain one. The request is signed
// with the installation key for the same reason a lease request is: without it,
// anyone who learned an installation identifier could queue requests in
// somebody else's name, and the operator approving from a dashboard would have
// no way to tell whose request it really was.

// requestSink records what the client sent when asking.
type requestSink struct {
	proofSeen   string
	nonceSeen   string
	installSeen string
	status      string
	code        int
}

func newRequestServer(t *testing.T, sink *requestSink) *Client {
	t.Helper()
	f := newFakeServer(t)

	// The fake's mux is already built, so the request route is added by
	// wrapping: anything the base fake does not answer falls through to here.
	f.server.Config.Handler = wrap(f.server.Config.Handler, "/v1/entitlements/request",
		func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				InstallationID string `json:"installation_id"`
				Nonce          string `json:"nonce"`
				Proof          string `json:"proof"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			sink.installSeen = body.InstallationID
			sink.nonceSeen = body.Nonce
			sink.proofSeen = body.Proof

			if sink.code != 0 && sink.code != http.StatusOK {
				w.WriteHeader(sink.code)
				return
			}
			status := sink.status
			if status == "" {
				status = "pending"
			}
			json.NewEncoder(w).Encode(map[string]any{"status": status, "tier": "ultra"})
		})

	return f.client(t)
}

// wrap serves one extra path in front of an existing handler.
func wrap(base http.Handler, path string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path {
			h(w, r)
			return
		}
		base.ServeHTTP(w, r)
	})
}

// The request must carry a proof, or the server cannot tell who is asking.
func TestRequestEntitlementProvesPossession(t *testing.T) {
	sink := &requestSink{}
	client := newRequestServer(t, sink)

	state, err := NewInstallation()
	if err != nil {
		t.Fatalf("installation: %v", err)
	}
	sessionID, _ := NewSessionID()

	status, err := client.RequestEntitlement(context.Background(), state, sessionID)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status is %q, want pending", status)
	}
	if sink.proofSeen == "" {
		t.Fatal("the request carried no proof of possession")
	}
	if sink.nonceSeen == "" {
		t.Fatal("the request carried no challenge nonce")
	}
	if sink.installSeen != state.InstallationID {
		t.Fatalf("request named %q, want %q", sink.installSeen, state.InstallationID)
	}
}

// Without the installation key there is nothing to sign with, so the request
// fails before anything is sent.
func TestRequestEntitlementNeedsTheInstallationKey(t *testing.T) {
	sink := &requestSink{}
	client := newRequestServer(t, sink)

	real, _ := NewInstallation()
	// An attacker with the identifier but not the key.
	stolen := State{InstallationID: real.InstallationID}

	if _, err := client.RequestEntitlement(context.Background(), stolen, "sess-x"); err == nil {
		t.Fatal("a request was made without the installation key")
	}
	if sink.proofSeen != "" {
		t.Fatal("a request reached the server without a usable key")
	}
}

// A refusal is reported as one. An installation that was revoked asking again
// must not look like a queued request.
func TestRequestEntitlementReportsRefusal(t *testing.T) {
	sink := &requestSink{code: http.StatusForbidden}
	client := newRequestServer(t, sink)

	state, _ := NewInstallation()
	if _, err := client.RequestEntitlement(context.Background(), state, "sess-x"); err == nil {
		t.Fatal("a 403 was reported as success")
	}
}

// The status is passed through rather than interpreted: the client does not
// decide what "active" means, it repeats what the server said.
func TestRequestEntitlementReportsActive(t *testing.T) {
	sink := &requestSink{status: "active"}
	client := newRequestServer(t, sink)

	state, _ := NewInstallation()
	status, err := client.RequestEntitlement(context.Background(), state, "sess-x")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if status != "active" {
		t.Fatalf("status is %q, want active", status)
	}
}
