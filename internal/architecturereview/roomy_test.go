package architecturereview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRoomyRendererEndpoint(t *testing.T) {
	r := builderReview(t, filepath.Join(t.TempDir(), "state"), nil, nil)
	token := "fixture-renderer-token-long-enough"
	before := stateFixture(t, r)
	for _, test := range []struct {
		method, body, auth string
		status             int
	}{
		{http.MethodPost, string(proposalFixture()), token, http.StatusOK},
		{http.MethodPost, `{}`, token, http.StatusBadRequest},
		{http.MethodGet, "", token, http.StatusMethodNotAllowed},
		{http.MethodPost, string(proposalFixture()), "", http.StatusForbidden},
	} {
		q := httptest.NewRequest(test.method, "http://localhost/api/roomy", strings.NewReader(test.body))
		q.Header.Set("Authorization", "Bearer "+test.auth)
		w := httptest.NewRecorder()
		r.Handler(token).ServeHTTP(w, q)
		if w.Code != test.status {
			t.Fatalf("%s: got %d: %s", test.method, w.Code, w.Body)
		}
		if w.Code == http.StatusOK {
			var body struct {
				SVG string `json:"svg"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body.SVG, `id="roomy-node-capture"`) {
				t.Fatal("shared renderer missing block anchor")
			}
		}
	}
	if !reflect.DeepEqual(before, stateFixture(t, r)) {
		t.Fatal("rendering changed the reviewed architecture")
	}
}
