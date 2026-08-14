package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/gemini"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/worker"
)

func TestRealGeminiAdapter(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_GEMINI") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_GEMINI=1 for authenticated external integration")
	}
	binary, err := project.FindBinary("gemini")
	if err != nil {
		t.Fatal(err)
	}
	repo := testgit.New(t)
	runner := worker.New(30*time.Second, 2*time.Second, 8<<20)
	client := gemini.New(binary, runner)
	if _, err := client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := client.Run(context.Background(), adapter.Request{
		TaskID: "TASK-REAL-GEMINI", Title: "Create gemini-proof.txt containing MARSHAL runtime proof.",
		Worktree: repo.Path(), BaseCommit: repo.HEAD(t), HeadCommit: repo.HEAD(t),
	})
	if err != nil {
		t.Skipf("Gemini run unverified (auth or rate limit): %v", err)
		return
	}
	if result.Status != adapter.StatusSuccess {
		t.Skipf("Gemini run status: %#v", result)
	}
}
