package integration

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/Zen1th53/slaves/internal/adapter"
	"github.com/Zen1th53/slaves/internal/adapter/codex"
	"github.com/Zen1th53/slaves/internal/testutil/testgit"
	"github.com/Zen1th53/slaves/internal/worker"
)

func TestRealCodexAdapter(t *testing.T) {
	if os.Getenv("SLAVES_TEST_REAL_CODEX") != "1" {
		t.Skip("set SLAVES_TEST_REAL_CODEX=1 for authenticated external integration")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	repo := testgit.New(t)
	runner := worker.New(3*time.Minute, 2*time.Second, 8<<20)
	client := codex.New(binary, runner)
	if _, err := client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := client.Run(context.Background(), adapter.Request{
		TaskID: "TASK-REAL", Title: "Create runtime-proof.txt containing exactly SLAVES runtime proof, then commit it.",
		Worktree: repo.Path(), BaseCommit: repo.HEAD(t), HeadCommit: repo.HEAD(t),
		AllowedOperations: []string{"filesystem.write", "shell.execute", "git.commit"},
		EvidenceRequired:  []string{"git status --short", "git log -1 --oneline"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != adapter.StatusSuccess {
		t.Fatalf("result = %#v", result)
	}
}
