package driver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHandInGitCannotExecuteWorkerSelectedCleanFilter(t *testing.T) {
	_, wt := newTask(t)
	marker := filepath.Join(t.TempDir(), "filter-ran")
	run(t, wt, "git", "config", "filter.host.clean", "touch "+marker+"; cat")
	if err := os.WriteFile(filepath.Join(wt, ".gitattributes"), []byte("*.txt filter=host\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "worker.txt"), []byte("worker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := git(context.Background(), wt, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("worker attributes executed host filter")
	}
}
