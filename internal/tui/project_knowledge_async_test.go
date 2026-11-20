//go:build linux

package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

func TestBoardShowsKnowledgeIndexProgressInFooter(t *testing.T) {
	w := openingTestWorkspace(t)
	w.launch.mu.Lock()
	w.launch.phase = launchReady
	w.launch.knowledgeProgress = "Indexing this project: 1,240 of 5,800 files"
	w.launch.mu.Unlock()

	lines, _, _ := w.frameLinesVitals(120, 30, false)
	frame := StripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(frame, "Indexing this project: 1,240 of 5,800 files") {
		t.Fatalf("expected indexing progress line in Board footer:\n%s", frame)
	}

	w.launch.mu.Lock()
	w.launch.knowledgeProgress = ""
	w.launch.mu.Unlock()

	lines, _, _ = w.frameLinesVitals(120, 30, false)
	frame = StripANSI(strings.Join(lines, "\n"))
	if strings.Contains(frame, "Indexing this project") {
		t.Fatalf("expected indexing progress line to clear after completion:\n%s", frame)
	}
}

func TestAppendProjectKnowledgeReportsStillIndexing(t *testing.T) {
	ws, rt := realControlWorkspace(t, "knowledge-still-indexing", false)
	root := rt.ProjectRoot()

	// Open a second deferred runtime (pending background index) and attach it
	// so appendProjectKnowledge observes active/pending indexing.
	deferred, err := app.OpenWithOptions(context.Background(), root, app.Options{DeferKnowledgeIndex: true})
	if err != nil {
		t.Fatal(err)
	}
	defer deferred.Close()
	ws.runtime = deferred

	origFetch := projectKnowledgeFetch
	called := false
	projectKnowledgeFetch = func(ctx context.Context, r *app.Runtime, root string) (string, error) {
		called = true
		return "should not block", nil
	}
	defer func() { projectKnowledgeFetch = origFetch }()

	brief := ws.appendProjectKnowledge(root, "Marshal protocol.")
	if called {
		t.Fatal("appendProjectKnowledge blocked calling projectKnowledgeFetch while indexing was pending")
	}
	if !strings.Contains(brief, "PROJECT KNOWLEDGE STILL INDEXING") {
		t.Fatalf("expected still-indexing notice, got:\n%s", brief)
	}
}
