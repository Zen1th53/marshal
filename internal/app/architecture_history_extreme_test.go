//go:build !governed

package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/architecturereview"
)

func TestAcceptedArchitectureOpensWithoutGoalOrModel(t *testing.T) {
	r := architectureServerRuntime(t)
	c, _ := requiredAcceptedContextFixture(t, r)
	// There is no active goal and this provider does not exist. The contract
	// itself must suffice, including after closing and reopening the view.
	for i := 0; i < 2; i++ {
		session, used, err := r.StartArchitectureReviewFromContract(t.Context(), "missing-provider", "", "", ArchitectureReviewHooks{})
		if err != nil || !used || session == nil {
			t.Fatalf("accepted architecture: used=%v session=%v err=%v", used, session, err)
		}
		state := architectureServerGET(t, session)
		if session.SourceContractDigest == "" {
			t.Fatal("the workspace cannot detect a superseded contract view")
		}
		if _, err := r.AcquireArchitectureReviewLease(); err == nil {
			t.Fatal("an accepted view did not keep its lease")
		} else if owned, ok := err.(*ArchitectureReviewOwnedError); !ok || owned.URL != session.URL {
			t.Fatalf("the lease advertises a different accepted view: %v", err)
		}
		if state["current"] != true || state["read_only"] != true || state["approved"] != false {
			t.Fatalf("inspection state: %+v", state)
		}
		graph := state["architecture"].(map[string]any)
		if graph["source_contract"].(map[string]any)["id"] != c.ID || graph["base_revision"] != float64(0) {
			t.Fatal(graph)
		}
		location, _ := url.Parse(session.URL)
		for _, path := range []string{"/api/history", "/api/history?revision=1", "/api/conformance"} {
			q, _ := http.NewRequest(http.MethodGet, "http://"+location.Host+path, nil)
			q.Header.Set("Authorization", "Bearer "+location.Query().Get("token"))
			response, err := http.DefaultClient.Do(q)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			err = json.NewDecoder(response.Body).Decode(&body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK {
				t.Fatalf("%s: %d %+v %v", path, response.StatusCode, body, err)
			}
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHistoryRevisionIncludesChangesAndCannotChangeCurrent(t *testing.T) {
	r := architectureServerRuntime(t)
	c, _ := requiredAcceptedContextFixture(t, r)
	config := architecturereview.Config{}
	r.configureArchitectureHistory(&config)
	raw, err := config.History(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	var entry struct {
		ReadOnly     bool `json:"read_only"`
		Architecture struct {
			BaseRevision int64                         `json:"base_revision"`
			Components   []struct{ ID, Change string } `json:"components"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatal(err)
	}
	if !entry.ReadOnly || entry.Architecture.BaseRevision != 0 || len(entry.Architecture.Components) != 1 || entry.Architecture.Components[0].Change != "new" {
		t.Fatalf("historical baseline: %s", raw)
	}
	if _, err := config.History(t.Context(), 99); err == nil {
		t.Fatal("unknown revision was served")
	}
	accepted, err := r.store.ReadAcceptedProjectContract(t.Context(), c.Project)
	if err != nil || accepted.Contract.Revision != c.Revision {
		t.Fatalf("history changed the active revision: %+v %v", accepted, err)
	}
}

func TestArchitectureProgressUsesDeliveredChangedPaths(t *testing.T) {
	r := architectureServerRuntime(t)
	requiredAcceptedContextFixture(t, r)
	r.recordArchitectureCommit(t.Context(), r.ProjectRoot(), "", "accepted", "")
	file := filepath.Join(r.ProjectRoot(), "internal", "core", "core.go")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package core\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, r.ProjectRoot(), "add", "internal")
	gitTest(t, r.ProjectRoot(), "commit", "-m", "Build core")
	r.recordArchitectureCommit(t.Context(), r.ProjectRoot(), "", "delivered", "")
	raw, err := r.architectureProgress(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var progress struct {
		Components []struct{ ID, Status string } `json:"components"`
	}
	if err := json.Unmarshal(raw, &progress); err != nil || len(progress.Components) != 1 || progress.Components[0].Status != "built" {
		t.Fatalf("built parts: %s %v", raw, err)
	}
}
