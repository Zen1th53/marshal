package mcp

import (
	"bytes"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/statepath"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectknowledge"
	"github.com/Zen1th53/marshal/internal/worktree"
)

func boundKnowledgeWorker(t *testing.T, rt *app.Runtime) (*Server, app.ClaimResult, string) {
	t.Helper()
	agent, err := rt.RegisterAgent(t.Context(), app.RegisterAgentRequest{Name: "knowledge worker", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	taskID := "TASK-knowledge"
	if _, err = rt.ImportTasks(t.Context(), []model.Task{{ID: taskID, Title: "Read project", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	task, err := rt.Task(t.Context(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := rt.Claim(t.Context(), app.ClaimRequest{TaskID: taskID, AgentID: agent.ID, ExpectedRevision: task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	head, err := exec.Command("git", "-C", rt.ProjectRoot(), "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := worktree.New(rt.ProjectRoot(), filepath.Join(rt.ProjectRoot(), statepath.DirName, "worktrees")).Prepare(t.Context(), model.WorktreeRequest{TaskID: taskID, Branch: "marshal/" + taskID, BaseCommit: strings.TrimSpace(string(head))})
	if err != nil {
		t.Fatal(err)
	}
	task, err = rt.Task(t.Context(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err = rt.Store().BeginExecution(t.Context(), taskID, claim.Session.ID, agent.ID, tree.Branch, tree.Path, tree.HEAD, task.Revision); err != nil {
		t.Fatal(err)
	}
	idx, err := projectknowledge.OpenWorktree(tree.Path, rt.ProjectRoot(), filepath.Join(rt.ProjectRoot(), statepath.DirName))
	if err != nil {
		t.Fatal(err)
	}
	if err = idx.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	return NewScopedServer(rt, "worker", taskID), claim, tree.Path
}

func TestKnowledgeWorkerLeaseAndSnapshotBinding(t *testing.T) {
	rt, root := knowledgeRuntime(t)
	worker, claim, tree := boundKnowledgeWorker(t, rt)
	// Owner-only edits must never replace the leased checkout's observations.
	if err := os.WriteFile(filepath.Join(root, "knowledge.go"), []byte("package sample\nfunc OwnerOnly(){}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, report, rpcErr := knowledgeWire(t, worker, 1, "find_symbol", map[string]any{"query": "東京"}, false)
	if rpcErr != nil || len(report.Result.Items) != 1 {
		t.Fatalf("bound checkout query: %+v %v", report, rpcErr)
	}
	foreign, _ := knowledgeRuntime(t)
	for _, args := range []map[string]any{
		{"query": "東京", "project_id": foreign.ProjectIdentity()},
		{"query": "東京", "task_id": "TASK-other"},
		{"query": "東京", "lease_id": "LEASE-other"},
		{"query": "東京", "root": foreign.ProjectRoot()},
		{"path": "../../knowledge.go"},
	} {
		name := "find_symbol"
		if _, ok := args["path"]; ok {
			name = "related_files"
		}
		if _, _, e := knowledgeWire(t, worker, 2, name, args, false); e == nil {
			t.Fatalf("scope escape accepted: %v", args)
		}
	}
	// A symlink at the bound checkout must fail closed.
	moved := tree + "-old"
	if err := os.Rename(tree, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, tree); err != nil {
		t.Fatal(err)
	}
	if _, _, e := knowledgeWire(t, worker, 4, "project_overview", nil, false); e == nil {
		t.Fatal("symlink checkout served source")
	}
	if err := os.Remove(tree); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, tree); err != nil {
		t.Fatal(err)
	}
	// A frozen server must refuse a released lease, even with a task role grant.
	task, err := rt.Task(t.Context(), "TASK-knowledge")
	if err != nil {
		t.Fatal(err)
	}
	if err = rt.Store().FinalizeExecution(t.Context(), task.ID, claim.Session.ID, true, task.Revision); err != nil {
		t.Fatal(err)
	}
	if _, _, e := knowledgeWire(t, worker, 3, "find_symbol", map[string]any{"query": "東京"}, false); e == nil {
		t.Fatal("released lease served source")
	}

}

func TestKnowledgeMCPProjectsStayIsolated(t *testing.T) {
	first, _ := knowledgeRuntime(t)
	second, root := knowledgeRuntime(t)
	if err := os.WriteFile(filepath.Join(root, "knowledge.go"), []byte("package sample\nfunc ForeignOnly(){}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, rt := range []*app.Runtime{first, second} {
		if _, _, e := knowledgeWire(t, NewScopedServer(rt, "marshal", ""), 1, "project_overview", map[string]any{"refresh": true}, false); e != nil {
			t.Fatal(e.Message)
		}
	}
	_, report, e := knowledgeWire(t, NewScopedServer(first, "marshal", ""), 2, "find_symbol", map[string]any{"query": "ForeignOnly"}, false)
	if e != nil || len(report.Result.Items) != 0 {
		t.Fatalf("foreign project leaked: %+v %v", report, e)
	}

}

func TestKnowledgeHTTPRequiresBoundIdentityCapabilityAndLease(t *testing.T) {
	rt, _ := knowledgeRuntime(t)
	worker, claim, _ := boundKnowledgeWorker(t, rt)
	manager := auth.NewManager(t.TempDir())
	token, _, err := manager.CreateToken(worker.workerBinding.AgentID, auth.KindMCPClient, []string{string(auth.CapTaskRead)})
	if err != nil {
		t.Fatal(err)
	}
	wrong, _, err := manager.CreateToken("foreign-agent", auth.KindMCPClient, []string{string(auth.CapTaskRead)})
	if err != nil {
		t.Fatal(err)
	}
	weak, _, err := manager.CreateToken(worker.workerBinding.AgentID, auth.KindMCPClient, []string{string(auth.CapStatusRead)})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithAuth(rt, manager)
	for _, tc := range []struct {
		token, lease string
		want         bool
	}{{token, claim.Lease.ID, true}, {token, "LEASE-other", false}, {token, "", false}, {wrong, claim.Lease.ID, false}, {weak, claim.Lease.ID, false}} {
		request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "find_symbol", "arguments": map[string]any{"task_id": worker.workerTaskID, "lease_id": tc.lease, "query": "東京"}}})
		in := httptest.NewRequest("POST", "/", bytes.NewReader(request))
		in.Header.Set("Authorization", "Bearer "+tc.token)
		out := httptest.NewRecorder()
		server.Handler().ServeHTTP(out, in)
		var response jsonRPCResponse
		if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if (response.Error == nil) != tc.want {
			t.Fatalf("HTTP binding result: want success %v, got %s", tc.want, out.Body.String())
		}
	}
}

func TestKnowledgeScopedTransportNeverGrantsTaskExecution(t *testing.T) {
	rt, _ := knowledgeRuntime(t)
	worker, _, _ := boundKnowledgeWorker(t, rt)
	for _, server := range []*Server{NewScopedServer(rt, "marshal", ""), worker} {
		for _, name := range []string{"task_run", "task_claim", "memory_remember", "task_memory_grant"} {
			if _, err := server.callTool(t.Context(), server.defaultCaller(), name, nil); err == nil {
				t.Fatalf("source transport granted %s", name)
			}
		}
		for _, tool := range server.listTools() {
			if !isKnowledgeTool(tool.Name) {
				t.Fatalf("source transport advertised authority tool %s", tool.Name)
			}
		}
	}
}
