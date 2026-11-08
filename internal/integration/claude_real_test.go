package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/worker"
)

func TestRealClaudeAdapter(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_CLAUDE=1 for authenticated external integration")
	}
	binary, err := project.FindBinary("claude")
	if err != nil {
		t.Fatal(err)
	}
	repo := testgit.New(t)
	runner := worker.New(3*time.Minute, 2*time.Second, 8<<20)
	client := claude.New(binary, runner)
	if _, err := client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := client.Run(context.Background(), adapter.Request{
		TaskID: "TASK-REAL-CLAUDE", Title: "Create claude-proof.txt containing exactly MARSHAL runtime proof followed by a newline.",
		Worktree: repo.Path(), BaseCommit: repo.HEAD(t), HeadCommit: repo.HEAD(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != adapter.StatusSuccess {
		t.Fatalf("Claude run status=%s exit=%d timed_out=%t stderr=%s", result.Status, result.ExitCode, result.TimedOut, result.Stderr)
	}
	proof, err := os.ReadFile(filepath.Join(repo.Path(), "claude-proof.txt"))
	if err != nil || string(proof) != "MARSHAL runtime proof\n" {
		t.Fatalf("Claude proof=%q err=%v", proof, err)
	}
}
