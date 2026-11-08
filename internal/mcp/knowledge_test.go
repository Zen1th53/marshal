package mcp

import (
	"bytes"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/statepath"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectknowledge"
)

func knowledgeRuntime(t *testing.T) (*app.Runtime, string) {
	t.Helper()
	repo := runtimeRepo(t)
	root := repo.Path()
	if err := os.WriteFile(filepath.Join(root, "knowledge.go"), []byte("package sample\nfunc 東京(){}\nfunc Call(){ 東京() }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# source 東京 "+strings.Repeat("<>&\"\\ proof ", 8)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "knowledge fixture"}} {
		cmd := exec.Command("/usr/bin/git", args...)
		cmd.Dir = root
		if _, err := cmd.CombinedOutput(); err != nil {
			t.Fatal("fixture commit failed")
		}
	}
	if _, err := app.Bootstrap(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime, root
}

func knowledgeWire(t *testing.T, server *Server, id any, name string, args map[string]any, http bool) ([]byte, knowledgeResponse, *rpcErr) {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	request, _ := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: params})
	var wire []byte
	if http {
		out := httptest.NewRecorder()
		server.Handler().ServeHTTP(out, httptest.NewRequest("POST", "/", bytes.NewReader(request)))
		wire = out.Body.Bytes()
	} else {
		var out bytes.Buffer
		if err := server.ServeStdio(t.Context(), bytes.NewReader(append(request, '\n')), &out); err != nil {
			t.Fatal(err)
		}
		wire = out.Bytes()
	}
	var envelope struct {
		Error  *rpcErr `json:"error"`
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(wire, &envelope); err != nil {
		t.Fatal("invalid knowledge wire")
	}
	if envelope.Error != nil {
		return wire, knowledgeResponse{}, envelope.Error
	}
	if len(envelope.Result.Content) != 1 {
		t.Fatal("missing knowledge content")
	}
	var result knowledgeResponse
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &result); err != nil {
		t.Fatal("invalid knowledge payload")
	}
	return wire, result, nil
}

func TestCommunityKnowledgeActualWireBudgetPaginationAndFreshness(t *testing.T) {
	runtime, root := knowledgeRuntime(t)
	server := NewScopedServer(runtime, "marshal", "")
	_, initial, err := knowledgeWire(t, server, 1, "project_overview", map[string]any{"refresh": true}, false)
	if err != nil {
		t.Fatal(err.Message)
	}
	if initial.Result.Snapshot.Stats.GoFiles == 0 || initial.Result.Snapshot.Head == "" {
		t.Fatal("production overview missing coverage")
	}
	if _, err := os.Stat(filepath.Join(root, statepath.DirName, "knowledge", "index-v1.db")); err != nil {
		t.Fatal("runtime did not use its project knowledge namespace")
	}

	for _, http := range []bool{false, true} {
		wire, report, rpcError := knowledgeWire(t, server, "quote 東京 <>&\"\\", "find_symbol", map[string]any{"query": "東京", "max_bytes": 4096}, http)
		if rpcError != nil {
			t.Fatal(rpcError.Message)
		}
		if len(wire) != report.Bytes || len(wire) > 4096 || len(report.Result.Items) != 1 || report.Result.Items[0].Path != "knowledge.go" || report.Result.Items[0].Line != 2 {
			t.Fatal("actual envelope budget or source citation failed")
		}
		wire, report, rpcError = knowledgeWire(t, server, "escaped", "project_search", map[string]any{"query": "source 東京", "max_bytes": 4096}, http)
		if rpcError != nil || len(report.Result.Items) == 0 || len(wire) != report.Bytes || len(wire) > 4096 {
			t.Fatal("escaped Unicode metadata wire bound failed")
		}
	}
	_, page, rpcError := knowledgeWire(t, server, 1, "project_search", map[string]any{"query": ".", "max_records": 1}, false)
	if rpcError != nil || !page.Truncated || page.Result.NextOffset != 1 {
		t.Fatal("bounded page did not mark remaining")
	}
	_, next, rpcError := knowledgeWire(t, server, 2, "project_search", map[string]any{"query": ".", "offset": page.Result.NextOffset, "indexed_head": page.Result.Snapshot.Head, "tree_digest": page.Result.Snapshot.Digest}, false)
	if rpcError != nil || len(next.Result.Items) == 0 {
		t.Fatal("exact snapshot pagination failed")
	}
	if _, _, rpcError = knowledgeWire(t, server, 2, "project_search", map[string]any{"query": ".", "offset": 1}, false); rpcError == nil {
		t.Fatal("unbound pagination accepted")
	}
	for _, args := range []map[string]any{{"query": "東京", "max_bytes": 256}, {"query": "東京", "root": "/"}, {"query": "東京", "current_head": "wrong-head"}, {"query": ""}} {
		if _, _, rpcError = knowledgeWire(t, server, 2, "find_symbol", args, false); rpcError == nil {
			t.Fatal("invalid/stale source query accepted")
		}
	}
	if _, _, rpcError = knowledgeWire(t, server, strings.Repeat("x", 5000), "find_symbol", map[string]any{"query": "東京"}, false); rpcError == nil {
		t.Fatal("huge request ID was presented as bounded success")
	}
	if err := os.WriteFile(filepath.Join(root, "knowledge.go"), []byte("package sample\nfunc NewFact(){}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, rpcError = knowledgeWire(t, server, 2, "find_symbol", map[string]any{"query": "東京"}, false); rpcError == nil || !strings.Contains(rpcError.Message, "stale") {
		t.Fatal("tracked dirty edit silently served old definitions")
	}
	_, updated, rpcError := knowledgeWire(t, server, 2, "project_overview", map[string]any{"refresh": true}, false)
	if rpcError != nil || updated.Result.Snapshot.Stats.Changed != 1 {
		t.Fatal("production incremental refresh failed")
	}
	if _, _, rpcError = knowledgeWire(t, server, 2, "project_search", map[string]any{"query": ".", "offset": 1, "indexed_head": page.Result.Snapshot.Head, "tree_digest": page.Result.Snapshot.Digest}, false); rpcError == nil {
		t.Fatal("old dirty-tree page token accepted")
	}
}

func TestCommunityKnowledgeWorkerScopeAndRefreshAuthority(t *testing.T) {
	runtime, root := knowledgeRuntime(t)
	before, err := os.ReadFile(filepath.Join(root, statepath.DirName, "knowledge", "index-v1.db"))
	if err != nil {
		t.Fatal(err)
	}
	marshal := NewScopedServer(runtime, "marshal", "")
	worker := NewScopedServer(runtime, "worker", "TASK-knowledge")
	if _, _, err := knowledgeWire(t, worker, 1, "project_overview", map[string]any{"refresh": true}, false); err == nil {
		t.Fatal("worker refreshed shared cache")
	}
	after, err := os.ReadFile(filepath.Join(root, statepath.DirName, "knowledge", "index-v1.db"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("denied worker changed derived state")
	}
	if _, _, err := knowledgeWire(t, marshal, 1, "project_overview", map[string]any{"refresh": true}, false); err != nil {
		t.Fatal(err.Message)
	}
	if _, _, err := knowledgeWire(t, worker, 1, "find_symbol", map[string]any{"query": "東京"}, false); err == nil {
		t.Fatal("ungranted worker read project source")
	}
	project, err := runtime.Store().Project(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.Store().RegisterAgent(t.Context(), model.Agent{ID: "worker", ProjectID: project.ID, DisplayName: "Fixture worker", Role: model.RoleDeveloper, Status: model.AgentRegistered}); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Store().PutRoleBinding(t.Context(), authz.RoleBinding{ID: "knowledge-grant", PrincipalID: "worker", Role: "task-member", ScopeID: "TASK-knowledge", BoundBy: "fixture-operator", BoundAt: time.Now().UTC(), PolicyDigest: "sha256:" + strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	if _, _, e := knowledgeWire(t, worker, 1, "find_symbol", map[string]any{"query": "東京"}, false); e == nil {
		t.Fatal("task grant without a lease read source")
	}
	worker, _, _ = boundKnowledgeWorker(t, runtime)
	_, report, rpcError := knowledgeWire(t, worker, 1, "find_symbol", map[string]any{"query": "東京"}, false)
	if rpcError != nil || len(report.Result.Items) != 1 {
		t.Fatal("existing task-authorized worker did not read source")
	}
	for _, args := range []map[string]any{{"query": "東京", "project_id": "FOREIGN"}, {"query": "東京", "task_id": "TASK-foreign"}} {
		if _, _, rpcError = knowledgeWire(t, worker, 1, "find_symbol", args, false); rpcError == nil {
			t.Fatal("worker scope escaped")
		}
	}
	readOnly := memoryPrincipal(auth.Principal{ID: "reader", Kind: auth.KindMCPClient, Capabilities: []string{string(auth.CapTaskRead)}})
	if _, err = runtime.ProjectKnowledge(t.Context(), readOnly, project.ID, "", true, projectknowledge.Query{Mode: "overview", Limit: 10}); err == nil {
		t.Fatal("read capability mutated shared index")
	}
	if _, err = runtime.ProjectKnowledge(t.Context(), authz.Principal{ID: "unauthorized"}, project.ID, "", false, projectknowledge.Query{Mode: "overview", Limit: 10}); err == nil {
		t.Fatal("principal without read authority queried source")
	}
	names := map[string]bool{}
	for _, tool := range worker.listTools() {
		names[tool.Name] = true
	}
	for _, name := range []string{"project_overview", "find_symbol", "references", "related_files", "project_search"} {
		if !names[name] || requiredCapabilityForTool(name) != auth.CapTaskRead {
			t.Fatal("knowledge tool listing/capability missing")
		}
	}
}
