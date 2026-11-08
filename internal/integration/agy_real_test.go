package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/worker"
)

func realAgyModel() string {
	if name := os.Getenv("MARSHAL_AGY_MODEL"); name != "" {
		return name
	}
	return antigravity.DefaultModel
}

func TestRealAgyAdapter(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_AGY") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_AGY=1 for authenticated Antigravity integration")
	}
	binary, err := project.FindBinary("agy")
	if err != nil {
		t.Fatal(err)
	}
	repo := testgit.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := antigravity.New(binary, worker.New(3*time.Minute, 2*time.Second, 8<<20))
	if _, err := client.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := client.Run(ctx, adapter.Request{
		TaskID: "TASK-REAL-AGY", Title: "Use your file tool to create agy-proof.txt containing exactly MARSHAL agy proof followed by one newline. Do not change other files or run git commands.",
		Worktree: repo.Path(), BaseCommit: repo.HEAD(t), HeadCommit: repo.HEAD(t), Model: realAgyModel(),
		AllowedOperations: []string{"filesystem.read", "filesystem.write"},
	})
	if err != nil || result.Status != adapter.StatusSuccess || result.SessionID == "" {
		t.Fatalf("agy status=%s exit=%d err=%v stdout=%s stderr=%s", result.Status, result.ExitCode, err, result.Stdout, result.Stderr)
	}
	proof, err := os.ReadFile(filepath.Join(repo.Path(), "agy-proof.txt"))
	if err != nil || string(proof) != "MARSHAL agy proof\n" {
		t.Fatalf("agy proof=%q err=%v", proof, err)
	}
}

func TestRealAgyNativeDriver(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_AGY") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_AGY=1 for authenticated Antigravity integration")
	}
	binary, err := project.FindBinary("agy")
	if err != nil {
		t.Fatal(err)
	}
	repo := testgit.New(t)
	task := marshal.Task{
		PlanTaskID: "REAL-AGY-NATIVE", Worker: "agy", Mode: marshal.Native,
		BaseCommit: repo.HEAD(t), Files: []string{"agy-proof.txt"}, Criteria: []string{"exact agy proof bytes"},
		Checks: []marshal.Check{{Command: `python3 -c 'from pathlib import Path; assert Path("agy-proof.txt").read_bytes() == b"MARSHAL agy proof\n"'`, Criteria: []string{"exact agy proof bytes"}}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	native := driver.Agy(binary)
	handle, err := native.Launch(ctx, driver.Request{Task: task, Worktree: repo.Path(), Model: realAgyModel(), Brief: "Use your file tool to create ONLY agy-proof.txt containing exactly MARSHAL agy proof followed by one newline. Do not run git commands; MARSHAL records the change after you finish."})
	if err != nil {
		t.Fatal(err)
	}
	handIn, err := native.Wait(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	if handIn.Provider != "agy" || handIn.Mode != marshal.Native || len(handIn.RuntimeObserved) == 0 || handIn.RuntimeObserved[0].ExitCode != 0 {
		t.Fatalf("native worker observation=%+v", handIn)
	}
	if err := marshal.ValidateHandIn(task, handIn); err != nil {
		t.Fatalf("agy hand-in validation: %v; observed=%+v", err, handIn.RuntimeObserved)
	}
	if len(handIn.CheckResults) != 1 || !handIn.CheckResults[0].Passed {
		t.Fatalf("agy exact-byte check=%+v", handIn.CheckResults)
	}
}
