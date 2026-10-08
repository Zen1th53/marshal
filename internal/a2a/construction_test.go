package a2a

import (
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultServerDeniesUnauthenticatedImport(t *testing.T) {
	for _, path := range []string{"/a2a/tasks", "/message:send"} {
		recorder := httptest.NewRecorder()
		NewServer(nil).Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: got %d, want configuration refusal", path, recorder.Code)
		}
	}
}

func TestAuthenticatedConstructionRequiresManager(t *testing.T) {
	if server, err := NewServerWithAuth(nil, nil); err == nil || server != nil {
		t.Fatal("missing manager accepted")
	}
}

func TestInsecureConstructionOnlyLoopback(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8081", "192.0.2.1:8081", ":8081", "example.com:8081", "localhost:8081"} {
		if server, err := NewInsecureServer(nil, address); err == nil || server != nil {
			t.Fatalf("insecure address accepted: %s", address)
		}
	}
	for _, address := range []string{"127.0.0.1:8081", "[::1]:8081"} {
		server, err := NewInsecureServer(nil, address)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/a2a/tasks", strings.NewReader(`{}`))
		request.RemoteAddr = "192.0.2.1:1234"
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("remote insecure peer: %d", recorder.Code)
		}
		recorder = httptest.NewRecorder()
		request = httptest.NewRequest(http.MethodPost, "/a2a/tasks", strings.NewReader(`{}`))
		request.RemoteAddr = "127.0.0.1:1234"
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("explicit insecure loopback reaches request validation: %d", recorder.Code)
		}
	}
}

func mustInsecureServer(t *testing.T, runtime *app.Runtime) *Server {
	t.Helper()
	s, err := NewInsecureServer(runtime, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func mustAuthenticatedServer(t *testing.T, runtime *app.Runtime, manager *auth.Manager) *Server {
	t.Helper()
	s, err := NewServerWithAuth(runtime, manager)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
