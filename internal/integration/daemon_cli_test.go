package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/slaves/internal/adapter"
	"github.com/Zen1th53/slaves/internal/api"
	"github.com/Zen1th53/slaves/internal/app"
	"github.com/Zen1th53/slaves/internal/cli"
	"github.com/Zen1th53/slaves/internal/model"
	"github.com/Zen1th53/slaves/internal/testutil/testgit"
)

func TestDaemonCLIEndToEnd(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	layout, err := app.Bootstrap(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := app.OpenWithOptions(context.Background(), repo.Path(), app.Options{
		Adapters: map[string]adapter.Adapter{"fake": fakeCommitAdapter{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- api.NewServer(runtime).Serve(ctx, layout.Socket) }()
	waitRuntimeSocket(t, layout.Socket)

	agentOutput := runCLI(t, repo.Path(), "--json", "agent", "register", "--name", "fake", "--role", "developer")
	var agent model.Agent
	decodeOutput(t, agentOutput, &agent)
	taskFile := filepath.Join(repo.Path(), "tasks.json")
	if err := os.WriteFile(taskFile, []byte(`[{"id":"TASK-001","title":"prove runtime","status":"ready","risk":"R1"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	runCLI(t, repo.Path(), "task", "import", taskFile)
	runCLI(t, repo.Path(), "task", "claim", "TASK-001")
	runCLI(t, repo.Path(), "task", "release", "TASK-001")
	runOutput := runCLI(t, repo.Path(), "--json", "run", "TASK-001", "--adapter", "fake", "--revision", "2")
	var result app.RunResult
	decodeOutput(t, runOutput, &result)
	if result.Status != "success" || result.ResultCommit == repo.HEAD(t) || result.Isolation.Level != model.IsolationProcessOnly {
		t.Fatalf("run = %#v", result)
	}
	taskOutput := runCLI(t, repo.Path(), "--json", "task", "show", "TASK-001")
	var task model.Task
	decodeOutput(t, taskOutput, &task)
	if task.Status != model.TaskReview || task.HeadCommit == nil || *task.HeadCommit != result.ResultCommit {
		t.Fatalf("task = %#v", task)
	}
	var events []model.Event
	decodeOutput(t, runCLI(t, repo.Path(), "--json", "events"), &events)
	if len(events) < 4 {
		t.Fatalf("events = %#v", events)
	}
	var artifacts []model.Artifact
	decodeOutput(t, runCLI(t, repo.Path(), "--json", "artifacts"), &artifacts)
	if len(artifacts) != 2 {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	var verification app.VerifyResult
	decodeOutput(t, runCLI(t, repo.Path(), "--json", "verify", "--", "git", "status", "--short"), &verification)
	if verification.ExitStatus != 0 || verification.OutputDigest == "" {
		t.Fatalf("verification = %#v", verification)
	}
}

func TestRuntimeFakeAdapterExecution(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.OpenWithOptions(context.Background(), repo.Path(), app.Options{Adapters: map[string]adapter.Adapter{"fake": fakeCommitAdapter{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	agent, err := runtime.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "fake", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{ID: "TASK-001", Title: "prove runtime", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), app.RunRequest{TaskID: "TASK-001", AgentID: agent.ID, Adapter: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.ResultCommit == repo.HEAD(t) {
		t.Fatalf("result = %#v", result)
	}
	task, err := runtime.Task(context.Background(), "TASK-001")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskReview {
		t.Fatalf("task = %#v", task)
	}
}

type fakeCommitAdapter struct{}

func (fakeCommitAdapter) Probe(context.Context) (adapter.Probe, error) {
	return adapter.Probe{Name: "fake", Available: true, Version: "test-1", Capabilities: map[string]string{"run": "native"}}, nil
}
func (fakeCommitAdapter) Run(_ context.Context, request adapter.Request) (adapter.Result, error) {
	path := filepath.Join(request.Worktree, "runtime-proof.txt")
	if err := os.WriteFile(path, []byte("SLAVES runtime proof\n"), 0o600); err != nil {
		return adapter.Result{}, err
	}
	now := time.Now().UTC()
	return adapter.Result{Adapter: "fake", AdapterVersion: "test-1", Status: adapter.StatusSuccess,
		ExitCode: 0, Stdout: []byte("fake stdout"), Stderr: []byte("fake stderr"),
		StartedAt: now.Add(-time.Second), EndedAt: now,
		Isolation: model.IsolationCapability{Level: model.IsolationProcessOnly, Available: true, Process: true, Network: true, Reason: "deterministic test adapter"}}, nil
}
func (fakeCommitAdapter) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusSuccess, nil
}
func (fakeCommitAdapter) Resume(context.Context, string, adapter.Request) (adapter.Result, error) {
	return adapter.Result{}, nil
}
func (fakeCommitAdapter) Capabilities() map[string]string               { return map[string]string{"run": "native"} }
func (fakeCommitAdapter) CollectEvidence(adapter.Result) map[string]any { return map[string]any{} }
func (fakeCommitAdapter) Shutdown(context.Context, string) error        { return nil }

func runCLI(t *testing.T, root string, args ...string) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := cli.Execute(context.Background(), root, args, bytes.NewReader(nil), &stdout, &stderr); code != 0 {
		t.Fatalf("slaves %v: code=%d stderr=%s", args, code, stderr.String())
	}
	return stdout.Bytes()
}

func decodeOutput(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
}

func waitRuntimeSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("runtime socket did not appear")
}

func runtimeIntegrationRepo(t *testing.T) *testgit.Repository {
	t.Helper()
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}
