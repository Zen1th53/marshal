package cloud

import (
	"context"
	"errors"
	"testing"
)

func TestAuthorizeRegistersOncePerInstallationAndEndpoint(t *testing.T) {
	f := newFakeServer(t)
	dir := t.TempDir()
	cfg := Config{Endpoint: f.server.URL}
	for i := 0; i < 2; i++ {
		auth := Authorize(context.Background(), cfg, dir, "1.0.0")
		if auth.Err != nil {
			t.Fatalf("authorize run %d: %v", i, auth.Err)
		}
		auth.Stop()
	}
	if got := f.registrations.Load(); got != 1 {
		t.Fatalf("registrations after two runs = %d, want 1", got)
	}

	store := NewStore(dir)
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	st.RegisteredEndpoint = "https://previous.example.test"
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	auth := Authorize(context.Background(), cfg, dir, "1.0.0")
	if auth.Err != nil {
		t.Fatal(auth.Err)
	}
	auth.Stop()
	if got := f.registrations.Load(); got != 2 {
		t.Fatalf("registrations after endpoint change = %d, want 2", got)
	}

	st, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	newInstallation, err := NewInstallation()
	if err != nil {
		t.Fatal(err)
	}
	newInstallation.RegisteredInstallationID = st.RegisteredInstallationID
	newInstallation.RegisteredEndpoint = st.RegisteredEndpoint
	if err := store.Save(newInstallation); err != nil {
		t.Fatal(err)
	}
	auth = Authorize(context.Background(), cfg, dir, "1.0.0")
	if auth.Err != nil {
		t.Fatal(auth.Err)
	}
	auth.Stop()
	if got := f.registrations.Load(); got != 3 {
		t.Fatalf("registrations after installation change = %d, want 3", got)
	}
}

func TestUnknownInstallationReregistersAndRetriesOnce(t *testing.T) {
	for _, stage := range []string{"challenge", "sessions"} {
		t.Run(stage, func(t *testing.T) {
			f := newFakeServer(t)
			dir := t.TempDir()
			cfg := Config{Endpoint: f.server.URL}
			first := Authorize(context.Background(), cfg, dir, "1.0.0")
			if first.Err != nil {
				t.Fatal(first.Err)
			}
			first.Stop()
			if stage == "challenge" {
				f.unknownChallenge.Store(1)
			} else {
				f.unknownSession.Store(1)
			}
			second := Authorize(context.Background(), cfg, dir, "1.0.0")
			if second.Err != nil {
				t.Fatal(second.Err)
			}
			second.Stop()
			if got := f.registrations.Load(); got != 2 {
				t.Fatalf("registrations = %d, want 2", got)
			}
			if got := f.leases.Load(); got != 2 {
				t.Fatalf("successful sessions = %d, want 2", got)
			}
		})
	}
}

func TestRegistrationRateLimit(t *testing.T) {
	f := newFakeServer(t)
	f.rateLimitRegistration.Store(true)
	auth := Authorize(context.Background(), Config{Endpoint: f.server.URL}, t.TempDir(), "1.0.0")
	if !errors.Is(auth.Err, ErrRateLimited) {
		t.Fatalf("want rate limited error, got %v", auth.Err)
	}
	if got := ClassifyError(auth.Err); got != ErrorRateLimited {
		t.Fatalf("error class = %s, want %s", got, ErrorRateLimited)
	}
}

func TestUnknownInstallationRetriesOnlyOnce(t *testing.T) {
	f := newFakeServer(t)
	dir := t.TempDir()
	cfg := Config{Endpoint: f.server.URL}
	first := Authorize(context.Background(), cfg, dir, "1.0.0")
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	first.Stop()
	f.unknownChallenge.Store(2)
	second := Authorize(context.Background(), cfg, dir, "1.0.0")
	if !errors.Is(second.Err, ErrUnknownInstallation) {
		t.Fatalf("want unknown installation after retry, got %v", second.Err)
	}
	if got := f.registrations.Load(); got != 2 {
		t.Fatalf("registrations = %d, want 2", got)
	}
}

func TestFailedReregistrationClearsState(t *testing.T) {
	f := newFakeServer(t)
	dir := t.TempDir()
	cfg := Config{Endpoint: f.server.URL}
	first := Authorize(context.Background(), cfg, dir, "1.0.0")
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	first.Stop()
	f.unknownChallenge.Store(1)
	f.rateLimitRegistration.Store(true)
	second := Authorize(context.Background(), cfg, dir, "1.0.0")
	if !errors.Is(second.Err, ErrRateLimited) {
		t.Fatalf("want rate limit after unknown installation, got %v", second.Err)
	}
	st, err := NewStore(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.RegisteredInstallationID != "" || st.RegisteredEndpoint != "" {
		t.Fatal("failed re-registration left the state marked registered")
	}
}
