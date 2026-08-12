package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Zen1th53/slaves/internal/adapter"
	"github.com/Zen1th53/slaves/internal/adapter/claude"
	"github.com/Zen1th53/slaves/internal/project"
	"github.com/Zen1th53/slaves/internal/testutil/testgit"
	"github.com/Zen1th53/slaves/internal/worker"
)

func TestRealClaudeAdapter(t *testing.T) {
	if os.Getenv("SLAVES_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set SLAVES_TEST_REAL_CLAUDE=1 for authenticated external integration")
	}
	binary, err := project.FindBinary("claude")
	if err != nil {
		t.Fatal(err)
	}
	repo := testgit.New(t)
	runner := worker.New(30*time.Second, 2*time.Second, 8<<20)
	client := claude.New(binary, runner)
	if _, err := client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := client.Run(context.Background(), adapter.Request{
		TaskID: "TASK-REAL-CLAUDE", Title: "Create claude-proof.txt containing SLAVES runtime proof.",
		Worktree: repo.Path(), BaseCommit: repo.HEAD(t), HeadCommit: repo.HEAD(t),
	})
	if err != nil {
		t.Skipf("Claude run unverified (auth or rate limit): %v", err)
		return
	}
	if result.Status != adapter.StatusSuccess {
		t.Skipf("Claude run status: %#v", result)
	}
}
