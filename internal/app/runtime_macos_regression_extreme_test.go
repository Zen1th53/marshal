//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestRuntimeBuildersDoNotRequireGovernedHarness(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	dir := t.TempDir()
	for _, p := range []string{"codex", "claude"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte("#!/bin/sh\necho 'Logged in using ChatGPT'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	for _, option := range r.architectureBuilders()(context.Background()) {
		if (option.ID == "codex" || option.ID == "claude") && !option.Available {
			t.Errorf("usable native %s: %+v", option.ID, option)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\necho 'Not logged in' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tracker := trackerFor(r)
	tracker.mu.Lock()
	delete(tracker.auth, "codex")
	tracker.mu.Unlock()
	for _, option := range r.architectureBuilders()(context.Background()) {
		if option.ID == "codex" && (option.Available || option.Reason != "Codex is not signed in - run `codex login`") {
			t.Errorf("explicitly signed-out provider remained available: %+v", option)
		}
	}
}

func TestRuntimeContractReviewRevisionSurvivesReplacement(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c := projectcontext.Contract{ID: "project", Revision: 1, Purpose: "Purpose", Components: []projectcontext.Component{{ID: "core", Responsibility: "Core", Paths: []string{"src"}}}}
	for i, revision := range []int64{1, 2, 2} {
		c.Purpose = fmt.Sprintf("Purpose draft %d", i)
		c.Revision = revision
		seed, err := contractReviewSeedFor(c, "digest", "English")
		if err != nil {
			t.Fatal(err)
		}
		if err := replaceSupersededContractReview(dir, seed); err != nil {
			t.Fatal(err)
		}
		initial, err := nextContractReviewRevision(dir)
		if err != nil {
			t.Fatal(err)
		}
		review, err := architecturereview.New(architecturereview.Config{Project: "project", ArchitectureID: "architecture-project", StateDir: dir, InitialRevision: initial, Proposal: seed.Architecture, UI: seed.UI})
		if err != nil {
			t.Fatal(err)
		}
		_ = review
		data, err := os.ReadFile(filepath.Join(dir, "state.json"))
		if err != nil {
			t.Fatal(err)
		}
		var state struct {
			Revision int64 `json:"revision"`
		}
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		if state.Revision != int64(i+1) {
			t.Errorf("revised contract %d recorded as browser revision %d", revision, state.Revision)
		}
	}
}
