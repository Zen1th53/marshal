package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/projectknowledge"
)

func TestKnowledgeIndexStatusProgressLineFormat(t *testing.T) {
	cases := []struct {
		status KnowledgeIndexStatus
		want   string
	}{
		{KnowledgeIndexStatus{Done: true}, ""},
		{KnowledgeIndexStatus{Indexing: true}, "Indexing this project…"},
		{KnowledgeIndexStatus{Pending: true}, "Indexing this project…"},
		{KnowledgeIndexStatus{Indexing: true, Phase: "index", Count: 1240, Total: 5800}, "Indexing this project: 1,240 of 5,800 files"},
		{KnowledgeIndexStatus{Indexing: true, Phase: "scan", Count: 1240, Total: 5800}, "Indexing this project: scanning 1,240 of 5,800 files"},
		{KnowledgeIndexStatus{Indexing: true, Phase: "index", Count: 42, Total: 100}, "Indexing this project: 42 of 100 files"},
	}
	for _, tc := range cases {
		if got := tc.status.ProgressLine(); got != tc.want {
			t.Errorf("%+v -> %q, want %q", tc.status, got, tc.want)
		}
	}
}

func TestDeferredKnowledgeIndexBackgroundBuildAndWait(t *testing.T) {
	repo := runtimeRepo(t)
	root := repo.Path()
	layout, err := BootstrapWithOptions(t.Context(), root, BootstrapOptions{DeferKnowledgeIndex: true})
	if err != nil {
		t.Fatal(err)
	}
	knowledgeDB := filepath.Join(layout.RuntimeDir, "knowledge", "index-v1.db")
	if _, err := os.Stat(knowledgeDB); !os.IsNotExist(err) {
		t.Fatalf("deferred bootstrap built index synchronously: %v", err)
	}

	rt, err := OpenWithOptions(t.Context(), root, Options{DeferKnowledgeIndex: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if st := rt.KnowledgeIndexStatus(); !st.Pending || st.Done {
		t.Fatalf("initial status = %+v, want Pending=true Done=false", st)
	}

	// Gate the real index build so we can test waiting and timeout behavior
	// deterministically while indexing is active.
	started := make(chan struct{})
	release := make(chan struct{})
	rt.knowledge.ensureFn = func(ctx context.Context, l project.Layout, prev ...string) error {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return ensureProjectKnowledge(ctx, l, prev...)
	}

	var mu sync.Mutex
	var updates []KnowledgeIndexStatus
	rt.StartKnowledgeIndex(t.Context(), func(st KnowledgeIndexStatus) {
		mu.Lock()
		updates = append(updates, st)
		mu.Unlock()
	})
	<-started

	principal := authz.Principal{
		ID:   "test-dev",
		Role: authz.Role{Name: "developer", Authorities: []authz.Authority{authz.AuthorityTaskPlan, authz.AuthoritySourceWrite}},
	}

	// A caller whose context expires while indexing is running gets
	// ErrKnowledgeIndexing naming that indexing is still in progress.
	shortCtx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	if _, err := rt.ProjectKnowledge(shortCtx, principal, rt.ProjectID(), "", false, projectknowledge.Query{Mode: "overview", Limit: 5}); !errors.Is(err, ErrKnowledgeIndexing) || !strings.Contains(err.Error(), "Indexing this project") {
		t.Fatalf("ProjectKnowledge while indexing = %v, want ErrKnowledgeIndexing with progress", err)
	}
	shortCtx2, cancel2 := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel2()
	if _, err := rt.ProjectContext(shortCtx2, principal, rt.ProjectID(), "", false, ""); !errors.Is(err, ErrKnowledgeIndexing) {
		t.Fatalf("ProjectContext while indexing = %v, want ErrKnowledgeIndexing", err)
	}

	// A caller that waits receives the built index as soon as indexing finishes.
	resultCh := make(chan error, 1)
	go func() {
		res, err := rt.ProjectKnowledge(t.Context(), principal, rt.ProjectID(), "", false, projectknowledge.Query{Mode: "overview", Limit: 5})
		if err == nil && res.Snapshot.Head == "" {
			err = errors.New("empty snapshot after wait")
		}
		resultCh <- err
	}()
	close(release)
	if err := <-resultCh; err != nil {
		t.Fatalf("ProjectKnowledge waiting for index failed: %v", err)
	}
	if st := rt.KnowledgeIndexStatus(); !st.Done || st.Indexing || st.ProgressLine() != "" {
		t.Fatalf("final status = %+v, want Done=true", st)
	}
	mu.Lock()
	gotUpdates := len(updates)
	mu.Unlock()
	if gotUpdates < 2 {
		t.Fatalf("expected progress updates, got %d", gotUpdates)
	}
}
