package integration

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/Zen1th53/slaves/internal/adapter"
	"github.com/Zen1th53/slaves/internal/adapter/gemini"
	"github.com/Zen1th53/slaves/internal/testutil/testgit"
	"github.com/Zen1th53/slaves/internal/worker"
)

func TestRealGeminiAdapter(t *testing.T) {
	if os.Getenv("SLAVES_TEST_REAL_GEMINI") != "1" {
		t.Skip("set SLAVES_TEST_REAL_GEMINI=1 for authenticated external integration")
	}
	binary, err := exec.LookPath("gemini")
	if err != nil {
		t.Fatal(err)
	}
	repo := testgit.New(t)
	runner := worker.New(30, 2, 8<<20)
	client := gemini.New(binary, runner)
	if _, err := client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := client.Run(context.Background(), adapter.Request{
		TaskID: "TASK-REAL-GEMINI", Title: "Create gemini-proof.txt containing SLAVES runtime proof.",
		Worktree: repo.Path(), BaseCommit: repo.HEAD(t), HeadCommit: repo.HEAD(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != adapter.StatusSuccess {
		t.Fatalf("result = %#v", result)
	}
}
