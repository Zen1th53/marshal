package architecturereview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerRejectsHostileLayoutAndPath(t *testing.T) {
	r := builderReview(t, filepath.Join(t.TempDir(), "state"), nil, nil)
	token := "fixture-security-token-long-enough"
	payload := `</script><script>alert(1)</script>`
	for _, tc := range []struct {
		path        string
		status      int
		contentType string
	}{
		{"/?token=" + token + "&layout=" + url.QueryEscape(payload), http.StatusBadRequest, "application/json; charset=utf-8"},
		{"/?token=" + token + "&layout=roomy&layout=" + url.QueryEscape(payload), http.StatusBadRequest, "application/json; charset=utf-8"},
		{"/" + url.PathEscape(payload), http.StatusForbidden, "application/json; charset=utf-8"},
		{"/?token=" + token, http.StatusOK, "text/html; charset=utf-8"},
		{"/?token=" + token + "&layout=roomy", http.StatusOK, "text/html; charset=utf-8"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			q := httptest.NewRequest(http.MethodGet, "http://localhost"+tc.path, nil)
			q.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.Handler(token).ServeHTTP(w, q)
			if w.Code != tc.status || w.Header().Get("Content-Type") != tc.contentType {
				t.Fatalf("status=%d content-type=%q body=%s", w.Code, w.Header().Get("Content-Type"), w.Body)
			}
			if strings.Contains(w.Body.String(), payload) {
				t.Fatal("response contains unescaped payload")
			}
		})
	}
}

func TestHandlerEscapesRequestDerivedJSONError(t *testing.T) {
	r := builderReview(t, filepath.Join(t.TempDir(), "state"), nil, nil)
	payload := `<script>alert(1)</script>`
	r.config.History = func(_ context.Context, _ int64) (json.RawMessage, error) {
		return nil, fmt.Errorf("unknown path %s", payload)
	}
	token := "fixture-security-token-long-enough"
	q := httptest.NewRequest(http.MethodGet, "http://localhost/api/history", nil)
	q.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.Handler(token).ServeHTTP(w, q)
	if w.Code != http.StatusNotFound || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("unexpected response: %d %v", w.Code, w.Header())
	}
	if strings.Contains(w.Body.String(), payload) {
		t.Fatal("response contains unescaped payload")
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "unknown path "+payload {
		t.Fatalf("escaped response changed JSON value: %v", body)
	}
}
