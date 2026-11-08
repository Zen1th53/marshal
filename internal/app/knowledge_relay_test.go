package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
	"github.com/Zen1th53/marshal/internal/worktree"
)

func TestKnowledgeBoundRelayEndToEnd(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "marshal")
	buildArgs := []string{"build"}
	if !isExtremeBuild {
		buildArgs = append(buildArgs, "-tags", "governed")
	}
	buildArgs = append(buildArgs, "-o", binary, "./cmd/marshal")
	build := exec.Command("go", buildArgs...)
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build relay: %v %s", err, output)
	}
	t.Setenv("MARSHAL_MCP_BINARY", binary)
	root := runtimeRepo(t).Path()
	if err := os.WriteFile(filepath.Join(root, "knowledge.go"), []byte("package sample\nfunc BoundSource(){}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, root, "add", ".")
	marshalGit(t, root, "commit", "-m", "source fixture")
	if _, err := Bootstrap(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	rt, err := Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	head, err := gitMarshal(t.Context(), root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := worktree.New(root, rt.layout.Worktrees).Prepare(t.Context(), model.WorktreeRequest{TaskID: "TASK-relay", Branch: "marshal/relay", BaseCommit: head})
	if err != nil {
		t.Fatal(err)
	}
	binding, revoke, err := rt.openKnowledgeReadLease(t.Context(), "TASK-relay", tree.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	ctx, cancel := context.WithTimeout(sessionmcp.WithWorker(context.Background(), root, binding.TaskID, binding.LeaseID), 20*time.Second)
	defer cancel()
	args, _, cleanup, err := sessionmcp.Configure(ctx, "codex", []string{"exec", "-"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var relayArgs []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "mcp_servers.marshal.args=") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(arg, "mcp_servers.marshal.args=")), &relayArgs); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(relayArgs) == 0 {
		t.Fatal("provider did not receive a relay")
	}
	requests := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "find_symbol", "arguments": map[string]any{"query": "BoundSource"}}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "find_symbol", "arguments": map[string]any{"query": "BoundSource", "project_id": "FOREIGN"}}},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "find_symbol", "arguments": map[string]any{"query": "BoundSource", "lease_id": "LEASE-other"}}},
	}
	var input bytes.Buffer
	for _, request := range requests {
		if err := json.NewEncoder(&input).Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.CommandContext(ctx, binary, relayArgs...)
	command.Stdin = &input
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bound relay: %v %s", err, output)
	}
	lines := bytes.Split(bytes.TrimSpace(output), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("relay responses: %s", output)
	}
	for i, line := range lines {
		var response struct {
			Error  json.RawMessage `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if len(response.Error) > 0 || !bytes.Contains(response.Result, []byte("BoundSource")) {
				t.Fatalf("source query failed: %s", line)
			}
		} else if len(response.Error) == 0 {
			t.Fatalf("scope mismatch succeeded: %s", line)
		}
	}
	revoke()
	if _, err := rt.WorkerKnowledgeLease(t.Context(), binding.TaskID, binding.LeaseID); err == nil {
		t.Fatal("completed provider kept its lease")
	}
}
