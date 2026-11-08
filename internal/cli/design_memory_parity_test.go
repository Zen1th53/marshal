package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/mcp"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestDesignMemoryCLIAndMCPParity(t *testing.T) {
	ctx := context.Background()
	repo := cliRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.NewManager(t.TempDir())
	token, _, err := manager.CreateToken("designer", auth.KindMCPClient, []string{string(auth.CapTaskExecute)})
	if err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServerWithAuth(rt, manager)
	for _, stage := range []string{"concepts_brief", "design_draft", "review_findings", "resolution", "approved_design"} {
		// Whitespace is part of the artifact digest, including terminal newlines.
		content := "  durable design content for " + stage + "\n"
		path := stage + ".md"
		sum := sha256.Sum256([]byte(content))
		digest := "sha256:" + hex.EncodeToString(sum[:])
		if err := os.WriteFile(filepath.Join(repo.Path(), path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := Execute(ctx, repo.Path(), []string{"--json", "egm", "record", "--stage", stage, "--provider", "codex", "--egm-version", "v0.1.0", "--file", path}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("CLI: %d %s", code, stderr.String())
		}
		var cliRecord model.MemoryRecordV2
		if err := json.Unmarshal(stdout.Bytes(), &cliRecord); err != nil {
			t.Fatal(err)
		}
		response := callMCPTool(t, server, token, "design_record", map[string]any{"stage": stage, "provider": "codex", "egm_version": "v0.1.0", "path": path, "content": content, "digest": digest})
		if response.Error != nil {
			t.Fatalf("MCP: %+v", response.Error)
		}
		var mcpRecord model.MemoryRecordV2
		if err := json.Unmarshal([]byte(response.Text()), &mcpRecord); err != nil {
			t.Fatal(err)
		}
		if cliRecord.Body != mcpRecord.Body || cliRecord.Source.Reference != mcpRecord.Source.Reference || cliRecord.Lifecycle != model.MemoryCandidate || mcpRecord.Lifecycle != model.MemoryCandidate {
			t.Fatalf("parity: CLI=%+v MCP=%+v", cliRecord, mcpRecord)
		}
		var provenance app.DesignProvenance
		if err := json.Unmarshal([]byte(mcpRecord.Source.Reference), &provenance); err != nil {
			t.Fatal(err)
		}
		if provenance.Digest != digest || provenance.Version != "v0.1.0" || provenance.Provider != "codex" || provenance.Path != path || provenance.Stage != stage {
			t.Fatalf("provenance: %+v", provenance)
		}
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	rt, err = app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	records, err := rt.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: rt.ProjectID(), Lifecycle: model.MemoryCandidate})
	if err != nil || len(records) != 10 {
		t.Fatalf("durable candidates=%d err=%v", len(records), err)
	}
	principal := authz.Principal{ID: "reader", Role: authz.Role{Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	recalled, err := rt.Memory().Recall(ctx, principal, app.RecallRequest{ProjectID: rt.ProjectID(), Query: "durable design"})
	if err != nil || len(recalled.Results) != 0 {
		t.Fatalf("candidate leaked to recall: %+v %v", recalled, err)
	}
}

func TestDesignMemoryMCPAuthorizationAndValidation(t *testing.T) {
	ctx := context.Background()
	repo := cliRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	manager := auth.NewManager(t.TempDir())
	readToken, _, _ := manager.CreateToken("reader", auth.KindMCPClient, []string{string(auth.CapTaskRead)})
	writeToken, _, _ := manager.CreateToken("writer", auth.KindMCPClient, []string{string(auth.CapTaskExecute)})
	server := mcp.NewServerWithAuth(rt, manager)
	args := func() map[string]any {
		return map[string]any{"stage": "approved_design", "provider": "claude", "egm_version": "builtin", "path": "design.md", "content": "ordinary design"}
	}
	if response := callMCPTool(t, server, readToken, "design_record", args()); response.Error == nil {
		t.Fatal("read token could write")
	}
	for _, change := range []func(map[string]any){
		func(a map[string]any) { a["stage"] = "promote" }, func(a map[string]any) { a["digest"] = "sha256:bad" }, func(a map[string]any) { a["provider"] = "" }, func(a map[string]any) { a["path"] = "../secret" }, func(a map[string]any) { a["project_id"] = "other" }, func(a map[string]any) { a["content"] = strings.Repeat("x", app.MaxDesignArtifactBytes+1) }, func(a map[string]any) { a["content"] = "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret" }, func(a map[string]any) { a["content"] = 123 },
	} {
		a := args()
		change(a)
		if content, _ := a["content"].(string); security.RedactsSecrets && strings.Contains(content, "PRIVATE KEY") {
			// Extreme records the design with the key removed instead of refusing it.
			continue
		}
		if response := callMCPTool(t, server, writeToken, "design_record", a); response.Error == nil {
			t.Fatalf("invalid arguments accepted: keys=%v", a["stage"])
		}
	}
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+readToken)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), `"name":"design_record"`) {
		t.Fatalf("tool not registered: %s", w.Body.String())
	}
	if response := callMCPTool(t, server, writeToken, "design_record", args()); response.Error != nil {
		t.Fatalf("builtin fallback record failed: %+v", response.Error)
	}
}

type designMCPResponse struct {
	Result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (r designMCPResponse) Text() string {
	if len(r.Result.Content) == 0 {
		return ""
	}
	return r.Result.Content[0].Text
}
func callMCPTool(t *testing.T, server *mcp.Server, token, name string, args map[string]any) designMCPResponse {
	t.Helper()
	data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	var response designMCPResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("status=%d body=%s err=%v", w.Code, w.Body.String(), err)
	}
	return response
}
