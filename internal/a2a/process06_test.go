package a2a

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProcess06A2ASurfaceRequiresAuthentication(t *testing.T) {
	s := NewServer(nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/a2a/verifications/v", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", w.Code)
	}
}
