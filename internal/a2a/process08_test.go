package a2a

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Process 08 peer surfaces are evidence-only. An unauthenticated peer cannot
// discover cycles or counterfactuals, and no wire path may mutate promotion or
// rollback state.
func TestProcess08A2ASurfacesAreAuthenticatedAndReadOnly(t *testing.T) {
	s := NewServer(nil)
	paths := []string{
		"/a2a/optimization-cycles/cycle-1",
		"/a2a/optimization-cycles/cycle-1/candidates",
		"/a2a/optimization-cycles/cycle-1/counterfactuals",
		"/a2a/optimization-cycles/cycle-1/manifests",
		"/a2a/optimization-cycles/cycle-1/canaries",
	}
	for _, path := range paths {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s status=%d, want 401", path, w.Code)
		}
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s status=%d, want 405", path, w.Code)
		}
	}
}
