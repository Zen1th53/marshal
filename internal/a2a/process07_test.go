package a2a

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A peer agent must authenticate before reading durable learning memory.
func TestProcess07A2ASurfaceRequiresAuthentication(t *testing.T) {
	s := NewServer(nil)
	for _, path := range []string{"/a2a/memory-commits/mc-1", "/a2a/learning-memory?project_id=proj-1"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s status=%d, want 401", path, w.Code)
		}
	}
}

// The learning endpoints are read-only. A write attempt must be refused by the
// method check rather than reaching the runtime.
func TestProcess07A2ASurfaceRejectsMutation(t *testing.T) {
	s := NewServer(nil)
	for _, path := range []string{"/a2a/memory-commits/mc-1", "/a2a/learning-memory"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s status=%d, want 405", path, w.Code)
		}
	}
}
