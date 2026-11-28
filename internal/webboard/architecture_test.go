package webboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"github.com/Zen1th53/marshal/internal/architectureworkspace"
)

func TestArchitectureEndpointAuthorization(t *testing.T) {
	s := &Server{addr: "127.0.0.1:12345", port: "12345", token: "secret"}
	called := false
	s.ConfigureArchitecture(func(context.Context) (architectureworkspace.Snapshot, error) {
		called = true
		return architectureworkspace.Snapshot{Title: "accepted"}, nil
	})
	for _, tc := range []struct {
		token, origin string
		want          int
	}{{"", "", 401}, {"wrong", "", 401}, {"secret", "http://example.com", 403}, {"secret", "", 200}} {
		req := httptest.NewRequest("GET", "http://127.0.0.1:12345/api/architecture", nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		if response.Code != tc.want {
			t.Fatalf("%+v: %d", tc, response.Code)
		}
		if response.Header().Get("Content-Security-Policy") != contentSecurityPolicy {
			t.Fatal("missing CSP")
		}
		if tc.want != http.StatusOK && called {
			t.Fatal("unauthenticated source read")
		}
	}
	if !called {
		t.Fatal("source never read")
	}
}
func TestArchitecturePageInteractions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	if out, err := exec.Command(node, "testdata/architecture.cjs").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}
