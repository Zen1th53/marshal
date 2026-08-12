package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/slaves/internal/app"
	"github.com/Zen1th53/slaves/internal/testutil/testgit"
)

func runtimeRepo(t *testing.T) *testgit.Repository {
	t.Helper()
	repo := testgit.New(t)
	sourceRoot := filepath.Join("..", "..")
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join(sourceRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestA2AServerAgentCardDiscovery(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewServer(runtime)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// GET /.well-known/agent.json
	resp, err := http.Get(ts.URL + "/.well-known/agent.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var card map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	if card["name"] != "SLAVES Runtime Agent" {
		t.Fatalf("unexpected agent name: %v", card["name"])
	}
	if card["protocol_version"] != "1.0.0" {
		t.Fatalf("expected A2A protocol_version 1.0.0, got %v", card["protocol_version"])
	}
}

func TestA2ATaskDelegation(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewServer(runtime)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	taskReq := map[string]any{
		"protocol_version": "1.0.0",
		"sender_id":        "remote-agent-1",
		"requested_role":   "developer",
		"task": map[string]any{
			"id":    "TASK-A2A-001",
			"title": "A2A delegated task",
		},
	}
	body, _ := json.Marshal(taskReq)
	resp, err := http.Post(ts.URL+"/a2a/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res["status"] != "imported" {
		t.Fatalf("expected task status imported, got %v", res["status"])
	}
}

func TestA2ARoleSpoofingDenied(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewServer(runtime)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Remote caller attempts to self-assign QA or AppSec role
	taskReq := map[string]any{
		"protocol_version": "1.0.0",
		"sender_id":        "remote-agent-1",
		"requested_role":   "appsec",
		"task": map[string]any{
			"id":    "TASK-A2A-002",
			"title": "Malicious AppSec task",
		},
	}
	body, _ := json.Marshal(taskReq)
	resp, err := http.Post(ts.URL+"/a2a/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for remote role spoofing, got %d", resp.StatusCode)
	}
}
