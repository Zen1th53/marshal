package architecturereview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistoryEndpointIsAuthenticatedAndReadOnly(t *testing.T) {
	r := newFixture(t, nil)
	called := int64(-1)
	r.config.History = func(_ context.Context, revision int64) (json.RawMessage, error) {
		called = revision
		return json.RawMessage(`{"revisions":[{"revision":2,"date":"2026-11-19","goal":"goal","run":"run","accepted_by":"owner"}]}`), nil
	}
	const token = "history-fixture-with-a-long-token"
	for _, tc := range []struct {
		method, path, bearer string
		want                 int
	}{
		{http.MethodGet, "/api/history", "", 403},
		{http.MethodGet, "/api/history", token, 200},
		{http.MethodGet, "/api/history?revision=2", token, 200},
		{http.MethodGet, "/api/history?revision=0", token, 400},
		{http.MethodGet, "/api/history?revision=no", token, 400},
		{http.MethodPost, "/api/history", token, 405},
	} {
		q := httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path, nil)
		q.Header.Set("Authorization", "Bearer "+tc.bearer)
		w := httptest.NewRecorder()
		r.Handler(token).ServeHTTP(w, q)
		if w.Code != tc.want {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
		}
	}
	if called != 2 {
		t.Fatalf("revision requested: %d", called)
	}
	r.config.ReadOnly = true
	q := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/review", nil)
	q.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.Handler(token).ServeHTTP(w, q)
	if w.Code != http.StatusConflict || stateFixture(t, r)["approved"] != false {
		t.Fatal("inspection created an acceptance")
	}
}
