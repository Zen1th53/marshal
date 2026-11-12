package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestValidateInitializedNeverCreatesMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	if err := ValidateInitialized(t.Context(), path); err == nil {
		t.Fatal("accepted missing canonical state")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("validation created canonical state: %v", err)
	}
}

// Two windows on one project: one validates the state while another process
// opens, writes and closes it. A busy moment in the other window is not
// damage, so validation must wait for it rather than report the files as
// damaged.
func TestValidateInitializedWaitsForAnotherWindowsWrites(t *testing.T) {
	ctx := context.Background()
	if path := os.Getenv("MARSHAL_TEST_VALIDATE_WRITER"); path != "" {
		validateWriterLoop(ctx, path)
		return
	}
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "proj-validate", Repository: "/repo/validate", DefaultBranch: "main", PackVersion: "6.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// SQLite locks are per process, so the other window must be another
	// process for its locks to meet this one the way two terminals do.
	var writers []*exec.Cmd
	for range 2 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestValidateInitializedWaitsForAnotherWindowsWrites$")
		cmd.Env = append(os.Environ(), "MARSHAL_TEST_VALIDATE_WRITER="+path)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		writers = append(writers, cmd)
	}
	t.Cleanup(func() {
		for _, cmd := range writers {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	failures, attempts := 0, 0
	var first error
	for stop := time.Now().Add(3 * time.Second); time.Now().Before(stop); attempts++ {
		if err := ValidateInitialized(ctx, path); err != nil {
			failures++
			if first == nil {
				first = err
			}
		}
	}
	if failures != 0 {
		t.Fatalf("validation failed %d of %d times while another window used the state: %v", failures, attempts, first)
	}
}

// validateWriterLoop is the other window: it opens the state, writes and
// migrates it the way a window opening the project does, and closes it.
func validateWriterLoop(ctx context.Context, path string) {
	for stop := time.Now().Add(5 * time.Second); time.Now().Before(stop); {
		writer, err := Open(ctx, path)
		if err != nil {
			continue
		}
		writer.Migrate(ctx)
		writer.Close()
	}
}
