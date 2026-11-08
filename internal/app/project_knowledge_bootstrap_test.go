package app

import (
	"github.com/Zen1th53/marshal/internal/projectknowledge"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectKnowledgeBuiltAtNewAndImportedProjectSetup(t *testing.T) {
	for _, kind := range []string{"new", "imported"} {
		t.Run(kind, func(t *testing.T) {
			repo := runtimeRepo(t)
			root := repo.Path()
			if kind == "imported" {
				marshalGit(t, root, "add", ".")
				marshalGit(t, root, "commit", "-m", "imported project")
			}
			layout, err := Bootstrap(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			idx, err := projectknowledge.Open(root, layout.RuntimeDir)
			if err != nil {
				t.Fatal(err)
			}
			before, err := idx.Query(t.Context(), projectknowledge.Query{Mode: "overview", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if before.Snapshot.Head == "" {
				t.Fatal("setup did not build index")
			}
			// Rebuild from canonical source without depending on the disposable cache.
			if err = os.RemoveAll(filepath.Join(layout.RuntimeDir, "knowledge")); err != nil {
				t.Fatal(err)
			}
			rt, err := Open(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			after, err := idx.Query(t.Context(), projectknowledge.Query{Mode: "overview", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if before.Snapshot.Digest != after.Snapshot.Digest {
				t.Fatal("rebuild changed source identity")
			}
		})
	}
}
