package webcontrol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
	"github.com/Zen1th53/marshal/internal/webcontrol"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

type verificationRuntime struct{ st *store.Store }

func (r verificationRuntime) Store() *store.Store { return r.st }
func TestWebVerificationReadsCanonicalStateAndRequiresAuth(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s := verification.Session{ID: "v", Version: 1, Binding: verification.Binding{ProjectID: "p", GoalID: "g", GoalRevision: 1, PlanID: "p1", PlanVersion: 1, RunID: "r", RunVersion: 1, TreeDigest: "tree", EnvironmentDigest: "env"}, State: verification.Blocked, Criteria: []verification.Criterion{{ID: "c"}}, RequiredChecks: map[string]verification.Status{}, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateVerificationSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1"}, verificationRuntime{st})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/verifications/v"
	unauth := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, path, nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauth=%d", unauth.Code)
	}
	code, err := server.Sessions().CreateOneTimeCode("auditor", "qa")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": code})
	login := httptest.NewRecorder()
	server.Handler().ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body)))
	cookie := login.Result().Cookies()[0]
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	got := httptest.NewRecorder()
	server.Handler().ServeHTTP(got, req)
	if got.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
	var decoded verification.Session
	if err := json.NewDecoder(got.Body).Decode(&decoded); err != nil || decoded.State != verification.Blocked {
		t.Fatalf("%+v %v", decoded, err)
	}
}
