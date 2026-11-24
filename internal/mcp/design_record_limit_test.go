package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/store"
)

// A design artifact of exactly the cap is recorded through the session
// adapter; one byte more is refused whole with a typed limit and an action
// for a smaller artifact, and nothing is stored.
func TestDesignRecordLimitAtCap(t *testing.T) {
	repo := runtimeRepo(t)
	layout, err := app.Bootstrap(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	manager := auth.NewManager(layout.RuntimeDir)
	token, _, err := CreateWorkerToken(manager, "codex", "design-limit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARSHAL_MCP_TOKEN", token)
	srv := NewSessionServer(runtime, manager)
	record := func(content string) *rpcErr {
		t.Helper()
		request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "design_record", "arguments": map[string]any{"stage": "design_draft", "provider": "codex", "egm_version": "builtin", "path": "design/large.md", "content": content}}})
		var out bytes.Buffer
		if err := srv.ServeStdio(context.Background(), bytes.NewReader(append(request, '\n')), &out); err != nil {
			t.Fatal(err)
		}
		var response struct {
			Error *rpcErr `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Error
	}
	count := func() int {
		records, err := runtime.Store().ListMemoryV2(context.Background(), store.MemoryQueryFilter{ProjectID: runtime.ProjectID()})
		if err != nil {
			t.Fatal(err)
		}
		return len(records)
	}
	over := record(strings.Repeat("d", app.MaxDesignArtifactBytes+1))
	if over == nil || over.Data == nil || over.Data.Code != "invalid_input" || over.Data.Limit == nil {
		t.Fatalf("one over: %+v", over)
	}
	limit := over.Data.Limit
	if limit.Kind != "design_record" || limit.ActualBytes != app.MaxDesignArtifactBytes+1 || limit.CapBytes != app.MaxDesignArtifactBytes || !strings.Contains(limit.Action, "smaller artifact") || !strings.Contains(over.Message, "smaller artifact") {
		t.Fatalf("one over limit: %+v %q", limit, over.Message)
	}
	if n := count(); n != 0 {
		t.Fatalf("oversized design stored %d records", n)
	}
	if err := record(strings.Repeat("d", app.MaxDesignArtifactBytes)); err != nil {
		t.Fatalf("at bound: %+v", err)
	}
	if n := count(); n != 1 {
		t.Fatalf("design at the bound stored %d records", n)
	}
}
