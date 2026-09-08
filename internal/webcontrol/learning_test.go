package webcontrol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/webcontrol"
)

type learningRuntime struct{ st *store.Store }

func (r learningRuntime) Store() *store.Store { return r.st }

func learningEntry() learning.Entry {
	return learning.Entry{
		ProjectID: "p", GoalID: "g", GoalRevision: 1, PlanID: "p1", PlanVersion: 1,
		RunID: "r", RunVersion: 1, VerificationID: "v", VerificationVersion: 1,
		AttestationDigest: "att", EvidenceDigest: "bundle",
		TreeDigest: "tree", EnvironmentDigest: "env",
		Outcome: learning.OutcomeVerifiedComplete,
	}
}

func seedLearningStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	fresh := learning.Item{
		ID: "m-fresh", Claim: "the build command is go build", Scope: learning.ScopeProject,
		ProjectID: "p", State: model.ClaimStateVerified, Version: 1,
		Evidence:   []learning.EvidenceRef{{ID: "e1", ClusterID: "c1", Digest: "d1", Kind: "test", Observed: now}},
		Provenance: "process-06", Binding: learningEntry(), RecordedAt: now,
	}
	contested := learning.Item{
		ID: "m-contested", Claim: "tests run in parallel", Scope: learning.ScopeProject,
		ProjectID: "p", State: model.ClaimStateContested, Version: 1,
		Contradicts: []string{"m-fresh"},
		Evidence:    []learning.EvidenceRef{{ID: "e2", ClusterID: "c2", Digest: "d2", Kind: "test", Observed: now}},
		Provenance:  "process-06", Binding: learningEntry(), RecordedAt: now,
	}
	record, err := learning.NewCommit(learning.Commit{
		ID: "mc-1", Binding: learningEntry(), Additions: []learning.Item{fresh, contested},
		Provenance: "test",
		Playbooks: []learning.PlaybookCandidate{{
			ID: "pb-1", Title: "rebuild", Scope: learning.ScopeProject, ProjectID: "p",
			Steps: []string{"go build ./..."},
		}},
		Observations: []learning.RoutingObservation{
			{TaskClass: "go", Provider: "codex", ProviderVersion: "0.9", Model: "m", Outcome: learning.OutcomeVerifiedComplete, Selected: true, EvidenceID: "e3", ClusterID: "c3", Observed: now},
			{TaskClass: "go", Provider: "codex", ProviderVersion: "0.9", Model: "m", Outcome: learning.OutcomeFailed, Selected: true, EvidenceID: "e4", ClusterID: "c4", Observed: now},
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendMemoryCommit(ctx, record); err != nil {
		t.Fatal(err)
	}
	return st
}

func authedLearningServer(t *testing.T, st *store.Store) (*webcontrol.Server, *http.Cookie) {
	t.Helper()
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1"}, learningRuntime{st})
	if err != nil {
		t.Fatal(err)
	}
	code, err := server.Sessions().CreateOneTimeCode("auditor", "qa")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": code})
	login := httptest.NewRecorder()
	server.Handler().ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body)))
	return server, login.Result().Cookies()[0]
}

func getAuthed(t *testing.T, server *webcontrol.Server, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	got := httptest.NewRecorder()
	server.Handler().ServeHTTP(got, req)
	return got
}

// Every Process 07 web route requires authentication.
func TestWebLearningRoutesRequireAuth(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1"}, learningRuntime{seedLearningStore(t)})
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{
		"/api/v1/learning/memory-commits/mc-1",
		"/api/v1/learning/memory?project_id=p",
		"/api/v1/learning/memory/m-fresh/provenance",
		"/api/v1/learning/routing-trust",
		"/api/v1/learning/playbook-candidates?project_id=p",
		"/api/v1/learning/fingerprints?project_id=p",
		"/api/v1/learning/replay-index",
		"/api/v1/learning/benchmarks",
	}
	for _, path := range paths {
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s status=%d, want 401", path, w.Code)
		}
	}
}

// The web surface serves the canonical memory commit, refusals included.
func TestWebServesCanonicalMemoryCommit(t *testing.T) {
	server, cookie := authedLearningServer(t, seedLearningStore(t))
	got := getAuthed(t, server, cookie, "/api/v1/learning/memory-commits/mc-1")
	if got.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
	var decoded learning.Commit
	if err := json.NewDecoder(got.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != "mc-1" || len(decoded.Additions) != 2 {
		t.Fatalf("commit = %+v", decoded)
	}
}

// A retrieved claim must arrive with its uncertainty attached: the contested
// item is returned, marked contradicted, and not usable.
func TestWebMemorySearchKeepsContradictionVisible(t *testing.T) {
	server, cookie := authedLearningServer(t, seedLearningStore(t))
	got := getAuthed(t, server, cookie, "/api/v1/learning/memory?project_id=p")
	if got.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
	var results []learning.Result
	if err := json.NewDecoder(got.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	var contested *learning.Result
	for i := range results {
		if results[i].Item.ID == "m-contested" {
			contested = &results[i]
		}
	}
	if contested == nil {
		t.Fatal("contested memory was hidden from retrieval")
	}
	if !contested.Contradicted || contested.Usable {
		t.Fatalf("contested result = %+v, want contradicted and unusable", *contested)
	}
}

// An unbounded query is refused rather than served as a full memory dump.
func TestWebMemorySearchRejectsUnboundedQuery(t *testing.T) {
	server, cookie := authedLearningServer(t, seedLearningStore(t))
	got := getAuthed(t, server, cookie, "/api/v1/learning/memory")
	if got.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 for an unbounded query", got.Code)
	}
}

// Routing trust served over the web must include the failed observation, not
// only the success.
func TestWebRoutingTrustIncludesFailures(t *testing.T) {
	server, cookie := authedLearningServer(t, seedLearningStore(t))
	got := getAuthed(t, server, cookie, "/api/v1/learning/routing-trust?task_class=go")
	if got.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
	var trust []learning.Trust
	if err := json.NewDecoder(got.Body).Decode(&trust); err != nil {
		t.Fatal(err)
	}
	if len(trust) != 1 {
		t.Fatalf("trust records = %d, want 1", len(trust))
	}
	if trust[0].Verified != 1 || trust[0].Failed != 1 {
		t.Fatalf("trust = %+v, want one verified and one failed", trust[0])
	}
	// Nothing was measured, so cost and latency must stay absent.
	if trust[0].MeasuredCostMicros != nil || trust[0].MeasuredLatencyMillis != nil {
		t.Fatal("unmeasured cost or latency was reported as a value")
	}
}

// A playbook candidate served over the web is never active.
func TestWebPlaybookCandidatesAreNeverActive(t *testing.T) {
	server, cookie := authedLearningServer(t, seedLearningStore(t))
	got := getAuthed(t, server, cookie, "/api/v1/learning/playbook-candidates?project_id=p")
	if got.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
	var candidates []learning.PlaybookCandidate
	if err := json.NewDecoder(got.Body).Decode(&candidates); err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	if candidates[0].Active {
		t.Fatal("a playbook candidate was served as active")
	}
}

// The learning surface exposes no mutation route: a write must not reach it.
func TestWebLearningSurfaceHasNoMutationRoute(t *testing.T) {
	server, cookie := authedLearningServer(t, seedLearningStore(t))
	for _, path := range []string{"/api/v1/learning/memory", "/api/v1/learning/memory-commits/mc-1"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte("{}")))
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("POST %s succeeded; learning memory must be read-only over HTTP", path)
		}
	}
}
