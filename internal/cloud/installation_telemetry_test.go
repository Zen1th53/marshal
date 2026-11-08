package cloud

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// HTTP requests and JSON use the real client and server handler boundary. The
// transport is in-memory so these tests also run where listening is forbidden.
type contractTransport func(*http.Request) (*http.Response, error)

func (f contractTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func contractClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	c, err := NewClient("http://127.0.0.1", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = contractTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		handler(w, r)
		return w.Result(), nil
	})
	return c
}
func assertFields(t *testing.T, got map[string]json.RawMessage, names ...string) {
	t.Helper()
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	if len(got) != len(allowed) {
		t.Errorf("wire fields = %v; want exactly %v", got, names)
	}
	for name := range got {
		if !allowed[name] {
			t.Errorf("extra wire field %q", name)
		}
	}
}

func TestInstallationTelemetryContractBothModes(t *testing.T) {
	for _, ultra := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "ultra"}[ultra], func(t *testing.T) {
			var registrations atomic.Int32
			events := make(chan map[string]json.RawMessage, 32)
			c := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/installations/register":
					var registration map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&registration); err != nil {
						t.Error(err)
					}
					assertFields(t, registration, "installation_id", "public_key", "client_version", "os", "arch")
					registrations.Add(1)
				case "/v1/telemetry/events":
					var batch []map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
						t.Error(err)
					}
					for _, e := range batch {
						events <- e
					}
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
				}
				io.WriteString(w, `{}`)
			})
			store := NewStore(t.TempDir())
			mode := &ActiveMode{}
			if ultra {
				gate := contractGate(t, false)
				st, err := store.TelemetryPreference(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				st.InstallationID = gate.installationID
				if err := store.Save(st); err != nil {
					t.Fatal(err)
				}
				mode.Attach(gate)
			}
			run := func() {
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() { defer close(done); runInstallationTelemetryClient(ctx, c, store, "1.2.3", mode, time.Hour) }()
				for _, kind := range []string{"app_start", "heartbeat"} {
					select {
					case e := <-events:
						assertFields(t, e, "kind", "installation_id", "client_version", "os", "arch", "mode", "occurred_at")
						var gotKind, gotMode, os, arch, version string
						json.Unmarshal(e["kind"], &gotKind)
						json.Unmarshal(e["mode"], &gotMode)
						json.Unmarshal(e["os"], &os)
						json.Unmarshal(e["arch"], &arch)
						json.Unmarshal(e["client_version"], &version)
						wantMode := "standard"
						if ultra {
							wantMode = "ultra"
						}
						if gotKind != kind || gotMode != wantMode || os != runtime.GOOS || arch != runtime.GOARCH || version != "1.2.3" {
							t.Errorf("unexpected fields: %s", e)
						}
					case <-time.After(2 * time.Second):
						t.Fatal("no event")
					}
				}
				cancel()
				<-done
			}
			run()
			run()
			if registrations.Load() != 1 {
				t.Fatalf("registrations = %d", registrations.Load())
			}
		})
	}
}

func TestInstallationStartIsSentOnceBeforeShortProcessExit(t *testing.T) {
	var events atomic.Int32
	c := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/installations/register":
		case "/v1/telemetry/events":
			var batch []map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
				t.Error(err)
			}
			if len(batch) != 2 {
				t.Errorf("startup batch size = %d, want app_start and heartbeat", len(batch))
			}
			for _, event := range batch {
				var kind string
				if err := json.Unmarshal(event["kind"], &kind); err != nil {
					t.Error(err)
				}
				if kind != string(KindAppStart) && kind != string(KindHeartbeat) {
					t.Errorf("unexpected startup kind %q", kind)
				}
				events.Add(1)
			}
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		io.WriteString(w, `{}`)
	})
	store := NewStore(t.TempDir())
	if !sendInstallationStartClient(context.Background(), c, store, "1.2.3", &ActiveMode{}) {
		t.Fatal("startup telemetry was not sent")
	}
	if got := events.Load(); got != 2 {
		t.Fatalf("startup events = %d, want 2", got)
	}

	// A successful foreground send is carried into the worker so it waits for
	// the normal heartbeat interval instead of reporting the start again.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runInstallationTelemetryClientStarted(ctx, c, store, "1.2.3", &ActiveMode{}, time.Hour, true)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	if got := events.Load(); got != 2 {
		t.Fatalf("worker duplicated startup events: %d", got)
	}
}

func TestInstallationStartRespectsOptOut(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, _, err := store.SetTelemetryEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	c := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{}`)
	})
	if sendInstallationStartClient(context.Background(), c, store, "1.2.3", &ActiveMode{}) {
		t.Fatal("startup telemetry sent after opt-out")
	}
	if calls.Load() != 0 {
		t.Fatalf("network calls after opt-out = %d", calls.Load())
	}
}

func TestInstallationTelemetryOptOutAndNewIdentity(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	before, err := store.TelemetryPreference(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	c := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		var batch []map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&batch)
		if len(batch) != 1 {
			t.Errorf("batch = %v", batch)
			return
		}
		assertFields(t, batch[0], "kind", "installation_id", "client_version", "os", "arch")
		var kind, id string
		json.Unmarshal(batch[0]["kind"], &kind)
		json.Unmarshal(batch[0]["installation_id"], &id)
		if kind != "telemetry_opt_out" || id != before.InstallationID {
			t.Errorf("opt-out=%v", batch)
		}
		calls.Add(1)
	})
	c.store = store
	for i := 0; i < 2; i++ {
		st, changed, err := store.SetTelemetryEnabled(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		if changed {
			c.SendOptOut(ctx, st)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("opt-out attempts=%d", calls.Load())
	}
	// A separate Store represents another already running process.
	reloaded, err := NewStore(filepath.Dir(store.Path())).TelemetryPreference(ctx)
	if err != nil || !reloaded.TelemetryDisabled {
		t.Fatal("preference did not persist", err)
	}
	reporter := NewReporter(c, before.InstallationID, "", "1.2.3", nil)
	reporter.Record(KindHeartbeat, TelemetryUltra, "")
	reporter.Flush(ctx)
	disabledCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	runInstallationTelemetryClient(disabledCtx, c, store, "1.2.3", &ActiveMode{}, time.Millisecond)
	cancel()
	if calls.Load() != 1 {
		t.Fatal("telemetry continued after disable")
	}
	after, changed, err := store.SetTelemetryEnabled(ctx, true)
	if err != nil || !changed || after.TelemetryDisabled || after.InstallationID == before.InstallationID || bytes.Equal(before.PrivateKey, after.PrivateKey) || after.RegisteredInstallationID != "" {
		t.Fatal("enable did not reset identity", err)
	}
	reporter.Record(KindHeartbeat, TelemetryUltra, "")
	reporter.Flush(ctx)
	if calls.Load() != 1 {
		t.Fatal("old identity sent events after enable")
	}
}

func TestInstallationTelemetryOfflineBoundedAndNotice(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	if !store.TakeTelemetryNotice(ctx) || NewStore(filepath.Dir(store.Path())).TakeTelemetryNotice(ctx) {
		t.Fatal("notice must be shown once per machine")
	}
	var attempts atomic.Int32
	c := contractClient(t, nil)
	c.http.Transport = contractTransport(func(r *http.Request) (*http.Response, error) {
		attempts.Add(1)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runInstallationTelemetryClient(runCtx, c, store, "1.2.3", &ActiveMode{}, time.Hour)
	}()
	deadline := time.After(time.Second)
	for attempts.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("no offline attempt")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker blocked on offline network")
	}
	if time.Since(start) > time.Second {
		t.Fatal("shutdown delayed")
	}
	// Explicit opt-out still has a deadline even if the caller has none.
	start = time.Now()
	c.SendOptOut(ctx, State{InstallationID: "inst-0123456789abcdef"})
	if time.Since(start) > time.Second {
		t.Fatal("opt-out exceeded its bound")
	}
}

// contractGate holds a real signed, verified lease; renewal may be due now.
func contractGate(t *testing.T, due bool) *Gate {
	t.Helper()
	signer, ring := newSigner(t, "contract")
	now := time.Now().UTC()
	claims := validClaims(now)
	claims.IssuedAt = now.Add(-time.Minute)
	claims.ExpiresAt = now.Add(time.Minute)
	if due {
		claims.IssuedAt = now.Add(-3 * time.Minute)
	}
	lease := signer.issue(t, claims, validBundle())
	gate := NewGate(testInstallation, testSession, ring, func() time.Time { return now })
	if err := gate.Adopt(lease); err != nil {
		t.Fatal(err)
	}
	return gate
}

func TestInstallationTelemetryLeaseLossOnNetworkFailure(t *testing.T) {
	gate := contractGate(t, true)
	c := contractClient(t, nil)
	c.http.Transport = contractTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	state, _ := NewInstallation()
	session := NewSession(c, gate, state, "sess-0123456789abcdef")
	done := make(chan struct{})
	go func() { session.Maintain(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("renewal did not fall back promptly")
	}
	if gate.Entitled() || !errors.Is(gate.Failure(), ErrUnreachable) {
		t.Fatal("offline renewal retained ULTRA", gate.Failure())
	}
	mode := &ActiveMode{}
	mode.Attach(gate)
	if mode.Mode() != TelemetryStandard {
		t.Fatal("mode did not follow the lease loss")
	}
}

func TestInstallationTelemetryConcurrentRegistration(t *testing.T) {
	store := NewStore(t.TempDir())
	st, err := store.TelemetryPreference(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	handler := func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := contractClient(t, handler)
			c.store = NewStore(filepath.Dir(store.Path()))
			if _, err := c.ensureRegistered(context.Background(), st, false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("registration calls=%d", calls.Load())
	}
}

func TestInstallationTelemetryHeartbeatLossAndNoRevival(t *testing.T) {
	gate := contractGate(t, false)
	gate.mu.RLock()
	lease := *gate.lease
	gate.mu.RUnlock()
	c := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ultra/heartbeat" {
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		assertFields(t, body, "installation_id", "session_id")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	h := NewHeartbeat(c, gate, nil, State{InstallationID: testInstallation}, testSession)
	if h.beat(context.Background()) || gate.Entitled() || !errors.Is(gate.Failure(), ErrUnreachable) {
		t.Fatal("heartbeat failure kept ULTRA")
	}
	if err := gate.Adopt(lease); err == nil {
		t.Fatal("in-flight renewal revived a disconnected session")
	}
}

func TestInstallationTelemetryModeRequiresExecutionAndLease(t *testing.T) {
	mode := &ActiveMode{}
	gate := contractGate(t, false)
	mode.Attach(gate)
	var enabled atomic.Bool
	mode.ObserveExecution(enabled.Load)
	if mode.Mode() != TelemetryStandard {
		t.Fatal("idle entitlement reported as active ULTRA")
	}
	enabled.Store(true)
	if mode.Mode() != TelemetryUltra {
		t.Fatal("active ULTRA was not reported")
	}
	gate.DegradeBecause(ErrUnreachable)
	if mode.Mode() != TelemetryStandard {
		t.Fatal("lost connection reported as ULTRA")
	}
}

func TestInstallationTelemetryRegistrationCannotUndoOptOut(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial", true: "retry"}[force], func(t *testing.T) {
			store := NewStore(t.TempDir())
			st, err := store.TelemetryPreference(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			c := contractClient(t, func(w http.ResponseWriter, r *http.Request) { close(entered); <-release })
			c.store = store
			done := make(chan error, 1)
			go func() { _, err := c.ensureRegistered(context.Background(), st, force); done <- err }()
			<-entered
			_, _, err = store.SetTelemetryEnabled(context.Background(), false)
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			current, err := store.Load()
			if err != nil || !current.TelemetryDisabled {
				t.Fatal("registration undid opt-out", err)
			}
		})
	}
}

func TestInstallationTelemetryAutoGrantIsTrackedAndStillNeedsLease(t *testing.T) {
	store := NewStore(t.TempDir())
	st, err := store.TelemetryPreference(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	session, _ := NewSessionID()
	c := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/installations/register":
			io.WriteString(w, `{}`)
		case "/v1/ultra/challenge":
			io.WriteString(w, `{"nonce":"contract-nonce"}`)
		case "/v1/entitlements/request":
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			assertFields(t, body, "installation_id", "session_id", "nonce", "proof", "tier")
			var proof string
			json.Unmarshal(body["proof"], &proof)
			signature, err := base64.RawURLEncoding.DecodeString(proof)
			public, _ := st.Public()
			if err != nil || !ed25519.Verify(public, []byte("marshal-cloud-pop:v1:"+st.InstallationID+":contract-nonce"), signature) {
				t.Error("request lacked proof of possession")
			}
			io.WriteString(w, `{"status":"active","tier":"ultra","request_id":"req-contract"}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})
	c.store = store
	if _, err := c.ensureRegistered(context.Background(), st, false); err != nil {
		t.Fatal(err)
	}
	requests := NewRequestStore(filepath.Dir(store.Path()))
	got, err := c.RequestEntitlementTracked(context.Background(), st, session, requests, 0)
	if err != nil || got.State != "active" || got.LocalAttemptID == "" {
		t.Fatal("auto-grant was not tracked", got, err)
	}
	saved, found, err := requests.Read(context.Background(), got.Key)
	if err != nil || !found || saved.LocalAttemptID != got.LocalAttemptID || saved.ServerRequestID != "req-contract" {
		t.Fatal("missing durable request", err)
	}
	if (&ActiveMode{}).Mode() != TelemetryStandard {
		t.Fatal("request observation conferred ULTRA authority")
	}
}

func TestInstallationTelemetryRunningProcessObservesEnable(t *testing.T) {
	store := NewStore(t.TempDir())
	original, _, err := store.SetTelemetryEnabled(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 32)
	c := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/telemetry/events" {
			var batch []Event
			if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
				t.Error(err)
			}
			for _, event := range batch {
				events <- event
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runInstallationTelemetryClient(ctx, c, store, "1.2.3", &ActiveMode{}, time.Millisecond)
	}()
	// Preference changes come through a separate store, as in another CLI process.
	enabled, _, err := NewStore(filepath.Dir(store.Path())).SetTelemetryEnabled(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []EventKind{KindAppStart, KindHeartbeat} {
		select {
		case event := <-events:
			if event.Kind != kind || event.InstallationID != enabled.InstallationID || event.InstallationID == original.InstallationID {
				t.Fatal("did not adopt re-enabled identity", event)
			}
		case <-time.After(time.Second):
			t.Fatal("running reporter did not resume after enable")
		}
	}
	cancel()
	<-done
}

func TestInstallationTelemetryDisabledEnrollmentIsSuppressed(t *testing.T) {
	store := NewStore(t.TempDir())
	st, _, err := store.SetTelemetryEnabled(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	c := contractClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	c.store = store
	c.telemetryOnly = true
	if _, err := c.ensureRegistered(context.Background(), st, false); err == nil || calls.Load() != 0 {
		t.Fatal("disabled background enrollment reached the server")
	}
	// Opt-out does not prevent a user from requesting required ULTRA authority.
	c.telemetryOnly = false
	if _, err := c.ensureRegistered(context.Background(), st, false); err != nil || calls.Load() != 1 {
		t.Fatal("telemetry preference prevented ULTRA enrollment", err)
	}
}

// Exercise the shipped endpoint through a fake server transport; this test
// never opens a socket or contacts the public service.
func TestPublicTelemetryEndpointAndOptOutNoNetwork(t *testing.T) {
	t.Setenv(EnvEndpoint, "")
	cfg := LoadConfig()
	if cfg.Endpoint != "https://marshal.blackhat.uz" {
		t.Fatal(cfg.Endpoint)
	}
	var calls int
	c, err := NewClient(cfg.Endpoint, "v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = contractTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "marshal.blackhat.uz" {
			t.Errorf("wrong endpoint %s", r.URL)
		}
		calls++
		w := httptest.NewRecorder()
		w.Write([]byte(`{}`))
		return w.Result(), nil
	})
	store := NewStore(t.TempDir())
	if !sendInstallationStartClient(t.Context(), c, store, "v0.2.0", &ActiveMode{}) || calls != 2 {
		t.Fatal("startup did not register and report", calls)
	}
	st, changed, err := store.SetTelemetryEnabled(t.Context(), false)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if sendInstallationStartClient(t.Context(), c, store, "v0.2.0", &ActiveMode{}) || calls != 2 {
		t.Fatal("opted-out startup sent traffic", calls)
	}
	c.SendOptOut(t.Context(), st)
	if calls != 3 {
		t.Fatal("missing explicit opt-out", calls)
	}
	t.Setenv(EnvEndpoint, "off")
	if LoadConfig().Enabled() {
		t.Fatal("environment opt-out ignored")
	}
}
