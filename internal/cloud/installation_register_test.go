package cloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// The installer registers the install once with a budget that survives a
// slow first connection, longer than the bound on a background attempt.
func TestRegisterInstallationSurvivesASlowFirstConnection(t *testing.T) {
	var registrations atomic.Int32
	c := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/installations/register" {
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		time.Sleep(telemetryTimeout + 300*time.Millisecond)
		registrations.Add(1)
		io.WriteString(w, `{}`)
	})
	store := NewStore(t.TempDir())
	st, err := store.TelemetryPreference(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := registerInstallationClient(context.Background(), c, store, st); !ok || err != nil {
		t.Fatalf("registered = %v, %v", ok, err)
	}
	// Registered once is registered: a second run sends nothing.
	if ok, err := registerInstallationClient(context.Background(), c, store, st); !ok || err != nil {
		t.Fatalf("second registration = %v, %v", ok, err)
	}
	if got := registrations.Load(); got != 1 {
		t.Fatalf("registrations = %d, want 1", got)
	}
}

func TestRegisterInstallationRespectsOptOutAndEndpointOff(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := NewStore(dir).SetTelemetryEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if ok, err := RegisterInstallation(context.Background(), Config{Endpoint: "http://127.0.0.1:9"}, dir, "1.2.3"); ok || err != nil {
		t.Fatalf("opted-out registration = %v, %v", ok, err)
	}
	if ok, err := RegisterInstallation(context.Background(), Config{}, t.TempDir(), "1.2.3"); ok || err != nil {
		t.Fatalf("registration with the endpoint off = %v, %v", ok, err)
	}
}

func kinds(t *testing.T, r *http.Request) []string {
	t.Helper()
	var batch []map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		t.Error(err)
	}
	var out []string
	for _, e := range batch {
		var kind string
		_ = json.Unmarshal(e["kind"], &kind)
		out = append(out, kind)
	}
	return out
}

// One failed attempt at start is retried within seconds, not after the full
// heartbeat interval, so a dropped connection does not hide an install.
func TestFailedTelemetryIsRetriedSoon(t *testing.T) {
	saved := telemetryRetry
	telemetryRetry = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
	defer func() { telemetryRetry = saved }()
	var attempts atomic.Int32
	started := make(chan struct{}, 4)
	c := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/installations/register":
			if attempts.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		case "/v1/telemetry/events":
			for _, k := range kinds(t, r) {
				if k == string(KindAppStart) {
					started <- struct{}{}
				}
			}
		}
		io.WriteString(w, `{}`)
	})
	store := NewStore(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runInstallationTelemetryClientStarted(ctx, c, store, "1.2.3", &ActiveMode{}, time.Hour, false)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatalf("no start reported after a failed first attempt (attempts %d)", attempts.Load())
	}
}

// The control centre inside tmux keeps the heartbeat after the launcher has
// gone, and never reports a second start.
func TestHeartbeatChildNeverReportsStart(t *testing.T) {
	beats := make(chan struct{}, 8)
	c := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/telemetry/events" {
			for _, k := range kinds(t, r) {
				if k == string(KindAppStart) {
					t.Error("the heartbeat child reported a start")
				}
				if k == string(KindHeartbeat) {
					beats <- struct{}{}
				}
			}
		}
		io.WriteString(w, `{}`)
	})
	store := NewStore(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runInstallationTelemetryClientStarted(ctx, c, store, "1.2.3", &ActiveMode{}, 20*time.Millisecond, true)
	for i := 0; i < 2; i++ {
		select {
		case <-beats:
		case <-time.After(3 * time.Second):
			t.Fatal("no heartbeat from the child")
		}
	}
}
