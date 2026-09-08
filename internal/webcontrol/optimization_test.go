package webcontrol_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/optimization"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func seedOptimizationCycle(t *testing.T) *store.Store {
	t.Helper()
	st := seedLearningStore(t)
	cycle, err := optimization.NewCycle(optimization.Cycle{
		ID: "opt-1", Binding: optimization.Entry{ProjectID: "p", MemoryCommitID: "mc-1", MemoryVersion: 1, MemoryDigest: "digest", SourceSHA: "sha", TreeDigest: "tree", EnvironmentHash: "env", Outcome: learning.OutcomeVerifiedComplete},
		Objectives: []optimization.Objective{{Name: "verified_success", HigherIsBetter: true, Weight: 1}}, Provenance: "test",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendOptimizationCycle(context.Background(), cycle); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestOptimizationWebRoutesRequireAuthentication(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1"}, learningRuntime{seedOptimizationCycle(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/optimization/cycles/opt-1", "/api/v1/optimization/cycles/opt-1/candidates", "/api/v1/optimization/cycles/opt-1/counterfactuals", "/api/v1/optimization/cycles/opt-1/manifests", "/api/v1/optimization/cycles/opt-1/canaries"} {
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401", path, w.Code)
		}
	}
}

func TestOptimizationWebServesDigestVerifiedCycle(t *testing.T) {
	server, cookie := authedLearningServer(t, seedOptimizationCycle(t))
	got := getAuthed(t, server, cookie, "/api/v1/optimization/cycles/opt-1")
	if got.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
	var cycle optimization.Cycle
	if err := json.NewDecoder(got.Body).Decode(&cycle); err != nil {
		t.Fatal(err)
	}
	if cycle.ID != "opt-1" || cycle.Digest == "" {
		t.Fatalf("cycle=%+v", cycle)
	}
}
