package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func seedRegisteredState(t *testing.T, dir, endpoint string) (*Store, State) {
	t.Helper()
	store := NewStore(dir)
	st, err := store.TelemetryPreference(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st.RegisteredInstallationID = st.InstallationID
	st.RegisteredEndpoint = endpoint
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	return store, st
}

// A real install whose server row was deleted still has RegisteredInstallationID
// set locally. When /v1/telemetry/events answers 404, the client must
// re-register once and retry the flush once.
func TestTelemetryHealsOn404UnknownInstallation(t *testing.T) {
	t.Run("startup_flush", func(t *testing.T) {
		var mu sync.Mutex
		known := map[string]bool{}
		var registrations, eventCalls atomic.Int32
		var accepted []Event

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/installations/register":
				var body struct {
					InstallationID string `json:"installation_id"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				mu.Lock()
				known[body.InstallationID] = true
				mu.Unlock()
				registrations.Add(1)
				io.WriteString(w, `{}`)
			case "/v1/telemetry/events":
				eventCalls.Add(1)
				var batch []Event
				if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
					t.Error(err)
				}
				mu.Lock()
				ok := len(batch) > 0 && known[batch[0].InstallationID]
				if ok {
					accepted = append(accepted, batch...)
				}
				mu.Unlock()
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"accepted": len(batch), "submitted": len(batch)})
			default:
				t.Errorf("unexpected route %s", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		store, st := seedRegisteredState(t, t.TempDir(), srv.URL)
		c, err := NewClient(srv.URL, "1.2.3")
		if err != nil {
			t.Fatal(err)
		}
		if !sendInstallationStartClient(context.Background(), c, store, "1.2.3", &ActiveMode{}) {
			t.Fatal("startup telemetry did not heal after 404")
		}
		if got := registrations.Load(); got != 1 {
			t.Fatalf("registrations = %d, want 1", got)
		}
		if got := eventCalls.Load(); got != 2 {
			t.Fatalf("event calls = %d, want 2 (404 + retry)", got)
		}
		mu.Lock()
		gotAccepted := len(accepted)
		isKnown := known[st.InstallationID]
		mu.Unlock()
		if !isKnown || gotAccepted != 2 {
			t.Fatalf("known=%v accepted=%d, want true and 2", isKnown, gotAccepted)
		}
		saved, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if saved.RegisteredInstallationID != st.InstallationID || saved.RegisteredEndpoint != srv.URL {
			t.Fatalf("state registration not preserved: %+v", saved)
		}
		// Subsequent telemetry succeeds directly with no extra registration.
		if !sendInstallationStartClient(context.Background(), c, store, "1.2.3", &ActiveMode{}) {
			t.Fatal("subsequent telemetry failed")
		}
		if got := registrations.Load(); got != 1 {
			t.Fatalf("registrations after steady-state send = %d, want 1", got)
		}
	})

	t.Run("heartbeat_loop", func(t *testing.T) {
		var mu sync.Mutex
		known := map[string]bool{}
		var registrations atomic.Int32
		healed := make(chan Event, 8)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/installations/register":
				var body struct {
					InstallationID string `json:"installation_id"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				mu.Lock()
				known[body.InstallationID] = true
				mu.Unlock()
				registrations.Add(1)
				io.WriteString(w, `{}`)
			case "/v1/telemetry/events":
				var batch []Event
				_ = json.NewDecoder(r.Body).Decode(&batch)
				mu.Lock()
				ok := len(batch) > 0 && known[batch[0].InstallationID]
				mu.Unlock()
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				for _, e := range batch {
					select {
					case healed <- e:
					default:
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"accepted": len(batch), "submitted": len(batch)})
			default:
				t.Errorf("unexpected route %s", r.URL.Path)
			}
		}))
		defer srv.Close()

		store, st := seedRegisteredState(t, t.TempDir(), srv.URL)
		c, err := NewClient(srv.URL, "1.2.3")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			runInstallationTelemetryClientStarted(ctx, c, store, "1.2.3", &ActiveMode{}, 15*time.Millisecond, true)
		}()
		for i := 0; i < 2; i++ {
			select {
			case ev := <-healed:
				if ev.Kind != KindHeartbeat || ev.InstallationID != st.InstallationID {
					t.Fatalf("unexpected healed event: %+v", ev)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("heartbeat %d did not heal after 404", i)
			}
		}
		cancel()
		<-done
		if got := registrations.Load(); got != 1 {
			t.Fatalf("registrations = %d, want 1", got)
		}
	})
}

// When the server keeps returning 200 for compatibility but signals
// {"unknown_installation": true}, the client must also re-register once and
// retry the flush once.
func TestTelemetryHealsOn200UnknownInstallation(t *testing.T) {
	for _, body := range []string{
		`{"unknown_installation":true}`,
		`{"accepted":0,"submitted":2,"unknown_installation":true}`,
	} {
		t.Run(body, func(t *testing.T) {
			var mu sync.Mutex
			known := map[string]bool{}
			var registrations, eventCalls atomic.Int32
			var accepted []Event

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/installations/register":
					var reg struct {
						InstallationID string `json:"installation_id"`
					}
					if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
						t.Error(err)
					}
					mu.Lock()
					known[reg.InstallationID] = true
					mu.Unlock()
					registrations.Add(1)
					io.WriteString(w, `{}`)
				case "/v1/telemetry/events":
					eventCalls.Add(1)
					var batch []Event
					if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
						t.Error(err)
					}
					mu.Lock()
					ok := len(batch) > 0 && known[batch[0].InstallationID]
					if ok {
						accepted = append(accepted, batch...)
					}
					mu.Unlock()
					if !ok {
						w.WriteHeader(http.StatusOK)
						io.WriteString(w, body)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{
						"accepted":             len(batch),
						"submitted":            len(batch),
						"unknown_installation": false,
					})
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
				}
			}))
			defer srv.Close()

			store, st := seedRegisteredState(t, t.TempDir(), srv.URL)
			c, err := NewClient(srv.URL, "1.2.3")
			if err != nil {
				t.Fatal(err)
			}
			if !sendInstallationStartClient(context.Background(), c, store, "1.2.3", &ActiveMode{}) {
				t.Fatal("startup telemetry did not heal on 200 unknown_installation")
			}
			if got := registrations.Load(); got != 1 {
				t.Fatalf("registrations = %d, want 1", got)
			}
			if got := eventCalls.Load(); got != 2 {
				t.Fatalf("event calls = %d, want 2 (unknown + retry)", got)
			}
			mu.Lock()
			gotAccepted := len(accepted)
			isKnown := known[st.InstallationID]
			mu.Unlock()
			if !isKnown || gotAccepted != 2 {
				t.Fatalf("known=%v accepted=%d, want true and 2", isKnown, gotAccepted)
			}
			// A healthy 200 with unknown_installation: false does not re-register.
			if !sendInstallationStartClient(context.Background(), c, store, "1.2.3", &ActiveMode{}) {
				t.Fatal("subsequent telemetry failed")
			}
			if got := registrations.Load(); got != 1 {
				t.Fatalf("registrations after healthy 200 = %d, want 1", got)
			}
		})
	}
}

// Opted-out installations and development builds must never send telemetry or
// register, even if the server would answer 404.
func TestTelemetryHealSkipsDisabledAndDevelopmentBuild(t *testing.T) {
	t.Run("telemetry_disabled", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		store, st := seedRegisteredState(t, t.TempDir(), srv.URL)
		st.TelemetryDisabled = true
		if err := store.Save(st); err != nil {
			t.Fatal(err)
		}

		c, err := NewClient(srv.URL, "1.2.3")
		if err != nil {
			t.Fatal(err)
		}
		if sendInstallationStartClient(context.Background(), c, store, "1.2.3", &ActiveMode{}) {
			t.Fatal("disabled installation sent startup telemetry")
		}
		if err := c.SendTelemetry(context.Background(), []Event{{Kind: KindHeartbeat, InstallationID: st.InstallationID}}); err != nil {
			t.Fatalf("disabled SendTelemetry returned %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		runInstallationTelemetryClientStarted(ctx, c, store, "1.2.3", &ActiveMode{}, 5*time.Millisecond, false)
		cancel()
		if got := calls.Load(); got != 0 {
			t.Fatalf("disabled installation made %d requests, want 0", got)
		}
	})

	t.Run("development_build", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		t.Setenv(EnvEndpoint, "")
		cfg := LoadConfig()
		if cfg.Enabled() || !cfg.DevelopmentBuild {
			t.Fatalf("dev build config = %+v, want disabled DevelopmentBuild", cfg)
		}
		dir := t.TempDir()
		if ok, err := RegisterInstallation(context.Background(), cfg, dir, "1.2.3"); ok || err != nil {
			t.Fatalf("dev RegisterInstallation = (%v, %v), want (false, nil)", ok, err)
		}
		cancel := StartInstallationTelemetry(context.Background(), cfg, dir, "1.2.3", &ActiveMode{})
		cancel()
		cancel = StartInstallationHeartbeat(context.Background(), cfg, dir, "1.2.3", &ActiveMode{})
		cancel()

		// Even a client pointing straight at the production endpoint with a
		// custom transport must not send or register in a development build.
		store, st := seedRegisteredState(t, dir, DefaultEndpoint)
		c, err := NewClient(DefaultEndpoint, "1.2.3")
		if err != nil {
			t.Fatal(err)
		}
		c.store = store
		c.telemetryOnly = true
		c.http.Transport = contractTransport(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			w := httptest.NewRecorder()
			w.WriteHeader(http.StatusNotFound)
			return w.Result(), nil
		})
		if err := c.SendTelemetry(context.Background(), []Event{{Kind: KindHeartbeat, InstallationID: st.InstallationID}}); !errors.Is(err, ErrDevelopmentBuild) {
			t.Fatalf("dev SendTelemetry err = %v, want ErrDevelopmentBuild", err)
		}
		if sendInstallationStartClient(context.Background(), c, store, "1.2.3", &ActiveMode{}) {
			t.Fatal("dev startup telemetry succeeded")
		}
		if got := calls.Load(); got != 0 {
			t.Fatalf("dev build made %d requests, want 0", got)
		}
	})
}

// Repeated unknown-installation responses must never loop and must never
// trigger more than one re-registration per process per hour.
func TestTelemetryHealAtMostOneReregisterPerHour(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "404", status: http.StatusNotFound},
		{name: "200_unknown", status: http.StatusOK, body: `{"unknown_installation":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var registrations, eventCalls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/installations/register":
					registrations.Add(1)
					io.WriteString(w, `{}`)
				case "/v1/telemetry/events":
					eventCalls.Add(1)
					w.WriteHeader(tc.status)
					io.WriteString(w, tc.body)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
				}
			}))
			defer srv.Close()

			base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
			var clockMu sync.Mutex
			now := base
			oldNow := telemetryNow
			telemetryNow = func() time.Time {
				clockMu.Lock()
				defer clockMu.Unlock()
				return now
			}
			t.Cleanup(func() { telemetryNow = oldNow })
			advance := func(d time.Duration) {
				clockMu.Lock()
				now = now.Add(d)
				clockMu.Unlock()
			}

			store, st := seedRegisteredState(t, t.TempDir(), srv.URL)
			c1, err := NewClient(srv.URL, "1.2.3")
			if err != nil {
				t.Fatal(err)
			}
			c1.store = store
			c1.telemetryOnly = true
			batch := []Event{{Kind: KindHeartbeat, InstallationID: st.InstallationID}}

			// First unknown response triggers one re-register and one retry.
			if err := c1.SendTelemetry(context.Background(), batch); !errors.Is(err, ErrUnknownInstallation) {
				t.Fatalf("first SendTelemetry err = %v, want ErrUnknownInstallation", err)
			}
			if got := registrations.Load(); got != 1 {
				t.Fatalf("registrations after first unknown = %d, want 1", got)
			}
			if got := eventCalls.Load(); got != 2 {
				t.Fatalf("event calls after first unknown = %d, want 2", got)
			}

			// A second client in the same process (e.g. background worker after
			// startup send) must still respect the per-process one-hour limit.
			c2, err := NewClient(srv.URL, "1.2.3")
			if err != nil {
				t.Fatal(err)
			}
			c2.store = store
			c2.telemetryOnly = true

			for _, step := range []time.Duration{10 * time.Minute, 20 * time.Minute, 29 * time.Minute} {
				advance(step)
				beforeEvents := eventCalls.Load()
				if err := c2.SendTelemetry(context.Background(), batch); !errors.Is(err, ErrUnknownInstallation) {
					t.Fatalf("throttled SendTelemetry err = %v, want ErrUnknownInstallation", err)
				}
				if got := registrations.Load(); got != 1 {
					t.Fatalf("registrations within one hour = %d, want 1", got)
				}
				if got := eventCalls.Load() - beforeEvents; got != 1 {
					t.Fatalf("throttled SendTelemetry made %d event calls, want 1 (no retry loop)", got)
				}
			}

			// Once one full hour has elapsed, a single re-registration is allowed again.
			advance(time.Minute)
			beforeEvents := eventCalls.Load()
			if err := c2.SendTelemetry(context.Background(), batch); !errors.Is(err, ErrUnknownInstallation) {
				t.Fatalf("post-hour SendTelemetry err = %v, want ErrUnknownInstallation", err)
			}
			if got := registrations.Load(); got != 2 {
				t.Fatalf("registrations after one hour = %d, want 2", got)
			}
			if got := eventCalls.Load() - beforeEvents; got != 2 {
				t.Fatalf("post-hour SendTelemetry made %d event calls, want 2", got)
			}

			// Another call shortly after stays capped at 2.
			advance(5 * time.Minute)
			if err := c1.SendTelemetry(context.Background(), batch); !errors.Is(err, ErrUnknownInstallation) {
				t.Fatalf("second-hour throttled err = %v, want ErrUnknownInstallation", err)
			}
			if got := registrations.Load(); got != 2 {
				t.Fatalf("registrations after second-hour throttle = %d, want 2", got)
			}
		})
	}

	t.Run("failed_reregister_respects_hour_cap_in_loop", func(t *testing.T) {
		savedRetry := telemetryRetry
		telemetryRetry = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}
		t.Cleanup(func() { telemetryRetry = savedRetry })

		var registrations atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/installations/register":
				registrations.Add(1)
				w.WriteHeader(http.StatusTooManyRequests)
			case "/v1/telemetry/events":
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		store, _ := seedRegisteredState(t, t.TempDir(), srv.URL)
		c, err := NewClient(srv.URL, "1.2.3")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
		defer cancel()
		runInstallationTelemetryClientStarted(ctx, c, store, "1.2.3", &ActiveMode{}, 5*time.Millisecond, false)
		if got := registrations.Load(); got != 1 {
			t.Fatalf("registrations during failing loop = %d, want 1", got)
		}
	})
}
