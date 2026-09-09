package cloud

import (
	"context"
	"testing"
	"time"
)

// Presence must be reported often enough that a healthy client is never counted
// as gone. The server's window is six minutes; anything close to that would make
// one slow request look like a departure.
func TestHeartbeatIntervalFitsTheServerWindow(t *testing.T) {
	const serverPresenceWindow = 6 * time.Minute
	if heartbeatInterval >= serverPresenceWindow {
		t.Fatalf("interval %v is not shorter than the presence window %v",
			heartbeatInterval, serverPresenceWindow)
	}
	// Two missed beats must still leave the client inside the window, so a
	// brief outage does not register as a departure.
	if 3*heartbeatInterval > serverPresenceWindow {
		t.Fatalf("interval %v leaves no room for missed beats within %v",
			heartbeatInterval, serverPresenceWindow)
	}
}

// A heartbeat with no entitlement must send nothing. Presence is a fact about
// ULTRA usage, and a Standard session has not asked the Cloud for anything.
func TestHeartbeatStopsWithoutEntitlement(t *testing.T) {
	f := newFakeServer(t)
	client := f.client(t)
	state, _ := NewInstallation()

	// A gate holding no lease.
	gate := NewGate(state.InstallationID, testSession, NewKeyRing(), nil)
	reporter := NewReporter(nil, state.InstallationID, testSession, "1.0.0", nil)
	hb := NewHeartbeat(client, gate, reporter, state, testSession)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() { hb.Run(ctx); close(done) }()

	// Stop must return promptly whether or not Run reached its loop. That is
	// the property under test: shutdown must not depend on a race with startup.
	stopped := make(chan struct{})
	go func() { hb.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit")
	}
	if f.beats.Load() != 0 {
		t.Fatalf("an unentitled session sent %d heartbeats", f.beats.Load())
	}
}

// Stop must be safe to call twice and must not hang; sessions end from several
// paths and a double Stop should not be a crash.
func TestHeartbeatStopIsIdempotent(t *testing.T) {
	f := newFakeServer(t)
	state, _ := NewInstallation()
	gate := NewGate(state.InstallationID, testSession, NewKeyRing(), nil)
	hb := NewHeartbeat(f.client(t), gate, nil, state, testSession)

	go hb.Run(context.Background())
	hb.Stop()
	hb.Stop()
}

// A nil heartbeat must be inert, so the offline path needs no special case.
func TestNilHeartbeatIsSafe(t *testing.T) {
	var hb *Heartbeat
	hb.Run(context.Background())
	hb.Stop()
}

// A cancelled context must end the heartbeat.
func TestHeartbeatHonoursContext(t *testing.T) {
	f := newFakeServer(t)
	state, _ := NewInstallation()
	gate := NewGate(state.InstallationID, testSession, NewKeyRing(), nil)
	hb := NewHeartbeat(f.client(t), gate, nil, state, testSession)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { hb.Run(ctx); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("heartbeat ignored context cancellation")
	}
}

// Authorization.Stop must tear everything down without hanging, in an order
// that lets shutdown events still be sent.
func TestAuthorizationStopIsOrderly(t *testing.T) {
	f := newFakeServer(t)
	ctx := context.Background()

	auth := Authorize(ctx, Config{Endpoint: f.server.URL, ExecutionEnabled: true},
		t.TempDir(), "1.0.0")
	if auth.Err != nil {
		t.Fatalf("authorize: %v", auth.Err)
	}
	if auth.Gate == nil || !auth.Gate.Entitled() {
		t.Fatal("authorization did not produce an entitled gate")
	}
	if auth.Reporter == nil || auth.Heartbeat == nil {
		t.Fatal("authorization did not wire telemetry and heartbeat")
	}

	auth.Start(ctx)

	done := make(chan struct{})
	go func() { auth.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Stop did not return")
	}
}

// The lifecycle events a successful activation should produce must actually be
// recorded, or the server learns nothing about activations that worked.
func TestAuthorizeRecordsLifecycleEvents(t *testing.T) {
	f := newFakeServer(t)
	auth := Authorize(context.Background(),
		Config{Endpoint: f.server.URL}, t.TempDir(), "1.0.0")
	if auth.Err != nil {
		t.Fatalf("authorize: %v", auth.Err)
	}
	defer auth.Stop()

	auth.Reporter.mu.Lock()
	kinds := map[EventKind]int{}
	for _, e := range auth.Reporter.queue {
		kinds[e.Kind]++
	}
	auth.Reporter.mu.Unlock()

	for _, want := range []EventKind{
		KindAppStart, KindRegistration, KindActivationOK, KindModeChange,
	} {
		if kinds[want] == 0 {
			t.Fatalf("no %s event was recorded", want)
		}
	}
}

// A failed activation must be reportable. Attaching the reporter only on
// success would mean the events worth having were the ones that could not be
// sent.
func TestFailedActivationIsStillReported(t *testing.T) {
	f := newFakeServer(t)
	f.refuse.Store(true)

	auth := Authorize(context.Background(),
		Config{Endpoint: f.server.URL}, t.TempDir(), "1.0.0")
	if auth.Err == nil {
		t.Fatal("a refusing server produced a successful authorization")
	}
	if auth.Gate != nil {
		t.Fatal("a refused activation produced a gate")
	}
	if auth.Reporter == nil {
		t.Fatal("a failed activation left no reporter, so the failure cannot be reported")
	}

	auth.Reporter.mu.Lock()
	defer auth.Reporter.mu.Unlock()
	var sawError bool
	for _, e := range auth.Reporter.queue {
		if e.Kind == KindErrorClass || e.Kind == KindActivationFail {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("a failed activation recorded no failure event")
	}
}
