package cloud

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeServer is a minimal Community Cloud, enough to exercise the client's
// decisions without pretending to be the real implementation.
type fakeServer struct {
	keyID  string
	priv   ed25519.PrivateKey
	server *httptest.Server

	// refuse makes the server answer 403, as it would for a revoked
	// entitlement. down makes it answer 503, as it would during an outage.
	refuse atomic.Bool
	down   atomic.Bool

	leases atomic.Int64
	beats  atomic.Int64
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	f := &fakeServer{keyID: "k-fake", priv: priv}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/keys", func(w http.ResponseWriter, r *http.Request) {
		if f.down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		pub := priv.Public().(ed25519.PublicKey)
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"key_id":     f.keyID,
				"public_key": base64.RawURLEncoding.EncodeToString(pub),
			}},
		})
	})
	mux.HandleFunc("/v1/installations/register", f.guard(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mux.HandleFunc("/v1/ultra/challenge", f.guard(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		json.NewEncoder(w).Encode(challenge{
			Nonce: "nonce-fixed", IssuedAt: now, ExpiresAt: now.Add(time.Minute),
		})
	}))
	mux.HandleFunc("/v1/ultra/sessions", f.guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			InstallationID string `json:"installation_id"`
			SessionID      string `json:"session_id"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.leases.Add(1)
		json.NewEncoder(w).Encode(f.mint(body.InstallationID, body.SessionID))
	}))
	mux.HandleFunc("/v1/ultra/heartbeat", f.guard(func(w http.ResponseWriter, r *http.Request) {
		f.beats.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	mux.HandleFunc("/v1/telemetry/events", f.guard(func(w http.ResponseWriter, r *http.Request) {
		var batch []Event
		json.NewDecoder(r.Body).Decode(&batch)
		json.NewEncoder(w).Encode(map[string]any{
			"accepted": len(batch), "submitted": len(batch),
		})
	}))

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// guard applies the refuse and down switches uniformly.
func (f *fakeServer) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case f.down.Load():
			w.WriteHeader(http.StatusServiceUnavailable)
		case f.refuse.Load():
			w.WriteHeader(http.StatusForbidden)
		default:
			next(w, r)
		}
	}
}

func (f *fakeServer) mint(installation, session string) Lease {
	now := time.Now().UTC()
	claims := Claims{
		JTI: "jti-fake", KeyID: f.keyID, EntitlementID: "ent-fake",
		InstallationID: installation, SessionID: session,
		ClientVersion: "1.0.0", Capabilities: []string{CapabilityDelegation},
		IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	}
	bundle := Bundle{
		PolicyDigest: "sha256:fake",
		RoutingTable: map[string]string{"primary": f.server.URL},
		IssuedFor:    installation,
	}
	input, _ := signingInput(claims, bundle)
	return Lease{
		Claims: claims, Bundle: bundle,
		Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.priv, input)),
	}
}

func (f *fakeServer) client(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(f.server.URL, "1.0.0")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

// A full session: keys, registration, lease, and an entitled gate.
func TestSessionStartProducesEntitlement(t *testing.T) {
	f := newFakeServer(t)
	ctx := context.Background()
	client := f.client(t)

	ring, err := client.FetchKeys(ctx)
	if err != nil {
		t.Fatalf("fetch keys: %v", err)
	}
	state, err := NewInstallation()
	if err != nil {
		t.Fatalf("installation: %v", err)
	}
	sessionID, _ := NewSessionID()

	if err := client.Register(ctx, state); err != nil {
		t.Fatalf("register: %v", err)
	}
	gate := NewGate(state.InstallationID, sessionID, ring, nil)
	session := NewSession(client, gate, state, sessionID)
	if err := session.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !gate.Entitled() {
		t.Fatal("a completed session did not produce entitlement")
	}
}

// A revoked entitlement is refused, and refusal is distinguishable from an
// outage so the caller can tell "no" from "try again".
func TestRefusalIsDistinctFromOutage(t *testing.T) {
	f := newFakeServer(t)
	ctx := context.Background()
	client := f.client(t)
	state, _ := NewInstallation()

	f.refuse.Store(true)
	_, err := client.StartSession(ctx, state, "sess-x")
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("want ErrRefused, got %v", err)
	}
	f.refuse.Store(false)

	f.down.Store(true)
	_, err = client.StartSession(ctx, state, "sess-x")
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("want ErrUnreachable for a 503, got %v", err)
	}
}

// A server that cannot be reached at all leaves the gate unentitled, which is
// the offline path: Standard keeps working, ULTRA does not appear.
func TestUnreachableServerLeavesStandard(t *testing.T) {
	f := newFakeServer(t)
	client := f.client(t)
	f.server.Close()

	state, _ := NewInstallation()
	gate := NewGate(state.InstallationID, "sess-x", NewKeyRing(), nil)
	session := NewSession(client, gate, state, "sess-x")

	if err := session.Start(context.Background()); err == nil {
		t.Fatal("a dead server produced a session")
	}
	if gate.Entitled() {
		t.Fatal("a dead server produced entitlement")
	}
	if gate.Mode() != "standard" {
		t.Fatal("a dead server did not leave the session in Standard")
	}
}

// Plain HTTP to a non-loopback host is refused. The lease is signed, so an
// eavesdropper cannot forge one, but they could observe who is using MARSHAL.
func TestClientRefusesInsecureEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"http://marshal.blackhat.uz",
		"http://example.com:8788",
		"ftp://marshal.blackhat.uz",
	} {
		if _, err := NewClient(endpoint, "1.0.0"); !errors.Is(err, ErrInsecureEndpoint) {
			t.Fatalf("endpoint %q was accepted: %v", endpoint, err)
		}
	}
	// Loopback over HTTP is allowed so the server can be exercised locally.
	for _, endpoint := range []string{"http://127.0.0.1:8788", "http://localhost:8788"} {
		if _, err := NewClient(endpoint, "1.0.0"); err != nil {
			t.Fatalf("loopback endpoint %q was refused: %v", endpoint, err)
		}
	}
	if _, err := NewClient("https://marshal.blackhat.uz", "1.0.0"); err != nil {
		t.Fatalf("https endpoint was refused: %v", err)
	}
}

// A server offering no usable keys is refused rather than trusted by default.
func TestFetchKeysRefusesEmptyRing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "1.0.0")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.FetchKeys(context.Background()); !errors.Is(err, ErrRefused) {
		t.Fatalf("want ErrRefused for an empty key set, got %v", err)
	}
}

// A lease issued by a server whose key this client does not trust is refused.
// This is the impersonation case: a different endpoint answering plausibly.
func TestLeaseFromImpersonatingServerIsRefused(t *testing.T) {
	real := newFakeServer(t)
	impostor := newFakeServer(t)
	ctx := context.Background()

	realRing, err := real.client(t).FetchKeys(ctx)
	if err != nil {
		t.Fatalf("fetch keys: %v", err)
	}
	state, _ := NewInstallation()
	sessionID, _ := NewSessionID()

	// The lease comes from the impostor; the ring is the real server's.
	lease, err := impostor.client(t).StartSession(ctx, state, sessionID)
	if err != nil {
		t.Fatalf("impostor session: %v", err)
	}
	gate := NewGate(state.InstallationID, sessionID, realRing, nil)
	if err := gate.Adopt(lease); err == nil {
		t.Fatal("a lease from an impersonating server was adopted")
	}
	if gate.Entitled() {
		t.Fatal("an impersonating server produced entitlement")
	}
}

// Renewal proves possession again rather than presenting the old lease, so a
// captured lease does not buy an indefinite extension.
func TestRenewalRequiresTheInstallationKey(t *testing.T) {
	f := newFakeServer(t)
	ctx := context.Background()
	client := f.client(t)

	state, _ := NewInstallation()
	if _, err := client.RenewLease(ctx, state, "sess-x"); err != nil {
		t.Fatalf("renew with the real key failed: %v", err)
	}

	// An attacker with the identifier but not the key cannot sign the
	// challenge, so Sign fails before anything is sent.
	stolen := State{InstallationID: state.InstallationID}
	if _, err := client.RenewLease(ctx, stolen, "sess-x"); !errors.Is(err, ErrState) {
		t.Fatalf("renewal without the key was not refused: %v", err)
	}
}

// Stop must be safe to call more than once; sessions end from several paths.
func TestSessionStopIsIdempotent(t *testing.T) {
	f := newFakeServer(t)
	state, _ := NewInstallation()
	gate := NewGate(state.InstallationID, "sess-x", NewKeyRing(), nil)
	session := NewSession(f.client(t), gate, state, "sess-x")

	session.Stop()
	session.Stop()
}

// Maintain must degrade on a refusal rather than holding ULTRA until the
// current lease happens to expire.
func TestMaintainDegradesOnRefusal(t *testing.T) {
	f := newFakeServer(t)
	ctx := context.Background()
	client := f.client(t)

	ring, err := client.FetchKeys(ctx)
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	state, _ := NewInstallation()
	sessionID, _ := NewSessionID()

	// A gate whose clock runs ahead so renewal is due immediately.
	clock := time.Now().UTC()
	gate := NewGate(state.InstallationID, sessionID, ring, func() time.Time { return clock })
	session := NewSession(client, gate, state, sessionID)
	if err := session.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !gate.Entitled() {
		t.Fatal("no entitlement after start")
	}

	// The entitlement is revoked, then maintenance runs. The clock is moved
	// past the renewal point rather than merely forward: RenewAt is always half
	// the *remaining* life, so advancing part-way moves the target too.
	f.refuse.Store(true)
	renewAt, ok := gate.RenewAt()
	if !ok {
		t.Fatal("no renewal scheduled")
	}
	clock = renewAt.Add(time.Second)

	done := make(chan struct{})
	go func() { session.Maintain(ctx); close(done) }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Maintain did not return after a refusal")
	}
	if gate.Entitled() {
		t.Fatal("entitlement survived a revocation")
	}
}
