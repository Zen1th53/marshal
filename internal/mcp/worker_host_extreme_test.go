//go:build !governed && unix

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

type boundCallerRunner struct {
	responses []jsonRPCResponse
	calls     int
}

func (r *boundCallerRunner) Run(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
	r.calls++
	result := adapter.ProcessResult{StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}
	if strings.Contains(strings.Join(cmd.Args, " "), "--help") {
		result.Stdout = []byte("--json --sandbox --ephemeral --ignore-user-config --cd")
		return result, nil
	}
	if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
		result.Stdout = []byte("1.0.0")
		return result, nil
	}
	var relayArgs []string
	for _, value := range cmd.Args {
		if strings.HasPrefix(value, "mcp_servers.marshal.args=") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(value, "mcp_servers.marshal.args=")), &relayArgs); err != nil {
				return result, err
			}
		}
	}
	var requests, responses string
	for i := 0; i+1 < len(relayArgs); i++ {
		if relayArgs[i] == "--requests" {
			requests = relayArgs[i+1]
		}
		if relayArgs[i] == "--responses" {
			responses = relayArgs[i+1]
		}
	}
	if requests == "" || responses == "" {
		return result, fmt.Errorf("provider did not receive generated worker relay")
	}
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"memory_recall","arguments":{"query":"source","task_id":"TASK-host-worker","max_bytes":4096},"_meta":{"principal_id":"marshal","role":"marshal"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"task_get","arguments":{"task_id":"TASK-foreign"}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memory_remember","arguments":{"title":"forged","body":"forged"}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"memory_recall","arguments":{"query":"source","principal_id":"marshal"}}}
`
	var output bytes.Buffer
	if err := sessionmcp.Relay(ctx, requests, responses, io.NopCloser(strings.NewReader(input)), &output); err != nil {
		return result, err
	}
	decoder := json.NewDecoder(&output)
	for decoder.More() {
		var response jsonRPCResponse
		if err := decoder.Decode(&response); err != nil {
			return result, err
		}
		r.responses = append(r.responses, response)
	}
	if err := os.WriteFile(filepath.Join(cmd.Dir, "output.txt"), []byte("actual transport fixture\n"), 0600); err != nil {
		return result, err
	}
	result.Stdout = []byte(`{"result":"done"}` + "\n")
	return result, nil
}

// The legacy imported-task route cannot supply approved required context.
// Refusal must precede Claim, provider probes, and generation of the worker relay.
func TestExtremeWorkerHostAuthenticClaimedCallerThroughRelay(t *testing.T) {
	ctx := WithWorkerHost(t.Context())
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runner := &boundCallerRunner{}
	provider := codex.NewWithModels("/prepared/codex", runner, []codex.ModelInfo{{Slug: "test-model"}}, "test-model")
	runtime, err := app.OpenWithOptions(ctx, repo.Path(), app.Options{Adapters: map[string]adapter.Adapter{"codex": provider}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	agent, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "real admitted worker", Role: model.RoleDeveloper, ModelProvider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-host-worker", Title: "write output.txt", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Run(ctx, app.RunRequest{TaskID: "TASK-host-worker", AgentID: agent.ID, Adapter: "codex"})
	assertLegacyWorkerRefused(t, runtime, "TASK-host-worker", err)
	if runner.calls != 0 || len(runner.responses) != 0 {
		t.Fatal("refused legacy route invoked provider relay")
	}

}

func TestExtremeWorkerHostFactoryRejectsMissingAndForeignBinding(t *testing.T) {
	if _, err := sessionmcp.WithBoundWorker(t.Context(), "/private", "TASK-own", nil); err == nil {
		t.Fatal("missing host server accepted")
	}
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	factory := sessionmcp.WorkerServerFactoryFrom(WithWorkerHost(t.Context()))
	if _, err := factory(sessionmcp.WorkerHostSpec{Runtime: runtime, Root: t.TempDir(), TaskID: "TASK-own", PrincipalID: "AGENT-own", Validate: func(context.Context) error { return nil }}); err == nil {
		t.Fatal("foreign root bound to runtime")
	}
}
