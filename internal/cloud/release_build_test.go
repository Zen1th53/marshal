package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// allowProductionEndpoint lets one test resolve the production endpoint. Any
// client built for it still refuses to send (see TestProductionClientRefusesInTests),
// so the test must supply a fake transport to observe traffic.
func allowProductionEndpoint(t *testing.T) {
	t.Helper()
	allowProductionInTests = true
	t.Cleanup(func() { allowProductionInTests = false })
}

func setReleaseBuild(t *testing.T, value string) {
	t.Helper()
	old := releaseBuild
	releaseBuild = value
	t.Cleanup(func() { releaseBuild = old })
}

// A test binary never resolves the production Community Cloud: an unset
// variable or the production URL spelled out both leave the client off.
func TestTestBinaryNeverResolvesProduction(t *testing.T) {
	setReleaseBuild(t, "true") // even a release-stamped test binary
	for _, value := range []string{"", "  ", DefaultEndpoint, "https://MARSHAL.blackhat.uz/", "https://marshal.blackhat.uz."} {
		t.Setenv(EnvEndpoint, value)
		cfg := LoadConfig()
		if cfg.Enabled() || !cfg.DevelopmentBuild {
			t.Errorf("%q resolved to %+v inside a test binary", value, cfg)
		}
	}
	if ReleaseBuild() {
		t.Error("a test binary reported itself as a release build")
	}
}

// A development build may still talk to a server it is pointed at.
func TestDevelopmentBuildKeepsExplicitEndpoint(t *testing.T) {
	t.Setenv(EnvEndpoint, "http://127.0.0.1:18743")
	cfg := LoadConfig()
	if cfg.Endpoint != "http://127.0.0.1:18743" || cfg.DevelopmentBuild {
		t.Fatalf("explicit endpoint lost: %+v", cfg)
	}
}

// Outside tests the ldflags flag alone decides: only a binary built by
// tools/build_release.sh resolves the production endpoint.
func TestProductionRequiresReleaseFlag(t *testing.T) {
	for _, tc := range []struct {
		inTest, override bool
		flag             string
		want             bool
	}{
		{false, false, "", false},
		{false, false, "false", false},
		{false, false, "1", false},
		{false, false, "true", true},
		{true, false, "true", false},
		{true, true, "", true},
	} {
		if got := productionAllowedFor(tc.inTest, tc.override, tc.flag); got != tc.want {
			t.Errorf("%+v -> %v, want %v", tc, got, tc.want)
		}
	}
}

// Belt and braces: a client constructed for the production host inside a test
// binary refuses every request without opening a connection, even when the
// test explicitly allowed resolving the endpoint.
func TestProductionClientRefusesInTests(t *testing.T) {
	allowProductionEndpoint(t)
	c, err := NewClient(DefaultEndpoint, "0.2.17")
	if err != nil {
		t.Fatal(err)
	}
	st, err := NewInstallation()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Register(context.Background(), st); !errors.Is(err, ErrUnreachable) || !strings.Contains(err.Error(), ErrDevelopmentBuild.Error()) {
		t.Fatalf("production register from a test: %v", err)
	}
}

// RegisterInstallation and the heartbeat send nothing from a development build
// with no explicit endpoint: the installer's `marshal telemetry register` from
// a dev binary is a silent no-op.
func TestDevelopmentBuildRegistersNothing(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	t.Setenv(EnvEndpoint, "")
	cfg := LoadConfig()
	ok, err := RegisterInstallation(context.Background(), cfg, t.TempDir(), "0.2.0")
	if ok || err != nil {
		t.Fatalf("dev build registered: %v %v", ok, err)
	}
	cancel := StartInstallationHeartbeat(context.Background(), cfg, t.TempDir(), "0.2.0", &ActiveMode{})
	cancel()
	cancel = StartInstallationTelemetry(context.Background(), cfg, t.TempDir(), "0.2.0", &ActiveMode{})
	cancel()
	if calls.Load() != 0 {
		t.Fatal("traffic sent")
	}
}

func TestIsProductionEndpoint(t *testing.T) {
	for in, want := range map[string]bool{
		DefaultEndpoint:                  true,
		"https://marshal.blackhat.uz/v1": true,
		"https://Marshal.Blackhat.UZ":    true,
		"http://127.0.0.1:8080":          false,
		"https://staging.blackhat.uz":    false,
		"":                               false,
	} {
		if got := IsProductionEndpoint(in); got != want {
			t.Errorf("%q: %v want %v", in, got, want)
		}
	}
}
