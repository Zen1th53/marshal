package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestT130MCPMemoryTools(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewServer(runtime)

	// 1. tools/list must include memory tools
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})

	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", w.Code)
	}

	var resp struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	hasRecall := false
	hasRemember := false
	hasStatus := false
	hasHandoffCreate := false
	hasHandoffConsume := false
	for _, tool := range resp.Result.Tools {
		if tool.Name == "memory_status" {
			hasStatus = true
			if err := validateMemoryToolSchema(tool.InputSchema); err != nil {
				t.Errorf("memory_status schema: %v", err)
			}
			properties, _ := tool.InputSchema["properties"].(map[string]any)
			property, _ := properties["project_id"].(map[string]any)
			if property["type"] != "string" {
				t.Errorf("memory_status project_id property = %#v, want string", property)
			}
		}
		if tool.Name == "memory_recall" {
			hasRecall = true
			// Extreme recall defaults project_id to the bound project.
			required := []string{"project_id", "query"}
			if !directCandidateRetention {
				required = []string{"query"}
			}
			if err := validateMemoryToolSchema(tool.InputSchema, required...); err != nil {
				t.Errorf("memory_recall schema: %v", err)
			}
		}
		if tool.Name == "memory_remember" {
			hasRemember = true
			// Default requires the project; Extreme binds the project to the
			// live Marshal launch, so only the content is required.
			required := []string{"project_id", "title", "body"}
			if !directCandidateRetention {
				required = []string{"title", "body"}
			}
			if err := validateMemoryToolSchema(tool.InputSchema, required...); err != nil {
				t.Errorf("memory_remember schema: %v", err)
			}
		}
		if tool.Name == "memory_handoff_create" {
			hasHandoffCreate = true
			if err := validateMemoryToolSchema(tool.InputSchema, "task_id", "target_role"); err != nil {
				t.Errorf("memory_handoff_create schema: %v", err)
			}
		}
		if tool.Name == "memory_handoff_consume" {
			hasHandoffConsume = true
			if err := validateMemoryToolSchema(tool.InputSchema, "handoff_id"); err != nil {
				t.Errorf("memory_handoff_consume schema: %v", err)
			}
		}
	}

	if !hasStatus || !hasRecall || !hasRemember || !hasHandoffCreate || !hasHandoffConsume {
		t.Fatalf("expected all memory tools in tools/list, got: %+v", resp.Result.Tools)
	}

	// 2. Call memory_recall tool via tools/call
	callBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "memory_recall",
			"arguments": map[string]any{
				"project_id": "PROJ-1",
				"query":      "SQLite WAL",
			},
		},
	})

	callReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(callBody))
	callW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(callW, callReq)

	if callW.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for memory_recall, got: %d", callW.Code)
	}
}

func validateMemoryToolSchema(schema map[string]any, required ...string) error {
	if schema["type"] != "object" {
		return errors.New("type must be object")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return errors.New("properties must be an object")
	}
	for _, name := range required {
		property, ok := properties[name].(map[string]any)
		if !ok || property["type"] != "string" {
			return fmt.Errorf("required property %q must be a string", name)
		}
		declared, ok := schema["required"].([]any)
		found := false
		if ok {
			for _, item := range declared {
				if item == name {
					found = true
					break
				}
			}
		}
		if !found {
			return fmt.Errorf("required property %q is missing from required list", name)
		}
	}
	return nil
}

func TestMCPMemoryRememberKeepsCandidateLifecycleAndStatusCountsProjectRecords(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	project, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(runtime)
	caller := auth.Principal{ID: "mcp-test"}
	principal := memoryPrincipal(caller)
	if len(principal.Role.Authorities) != 1 || principal.Role.Authorities[0] != authz.AuthorityTaskPlan {
		t.Fatalf("unauthenticated local MCP memory principal has excessive authorities: %+v", principal.Role.Authorities)
	}
	remembered, err := srv.callTool(ctx, caller, "memory_remember", map[string]any{
		"project_id": project.ID,
		"title":      "MCP candidate",
		"body":       "Remembered facts remain candidates pending review.",
	})
	if !directCandidateRetention {
		// Extreme: a standalone MCP caller can never retain memory; only the
		// bound live Marshal proposes it. Nothing is persisted or counted.
		if err == nil || remembered != "" {
			t.Fatalf("standalone Marshal memory_remember = %q, %v; want refusal", remembered, err)
		}
		if got := runtime.ContinuationCandidates(); len(got) != 0 {
			t.Fatalf("refused remember left candidates: %+v", got)
		}
		all, listErr := runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: project.ID})
		if listErr != nil || len(all) != 0 {
			t.Fatalf("refused remember persisted records=%d err=%v", len(all), listErr)
		}
		statusJSON, statusErr := srv.callTool(ctx, caller, "memory_status", map[string]any{"project_id": project.ID})
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		var status struct {
			Records       int    `json:"records"`
			SchemaVersion string `json:"schema_version"`
		}
		if statusErr = json.Unmarshal([]byte(statusJSON), &status); statusErr != nil || status.Records != 0 || status.SchemaVersion == "" {
			t.Fatalf("status after refused remember = %+v err=%v", status, statusErr)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		ID        string `json:"id"`
		Lifecycle string `json:"lifecycle"`
	}
	if err := json.Unmarshal([]byte(remembered), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID == "" || result.Lifecycle != string(model.MemoryCandidate) {
		t.Fatalf("remember result = %+v, want an unapproved candidate", result)
	}
	candidates := runtime.ContinuationCandidates()
	var candidate *model.MemoryRecordV2
	for i := range candidates {
		if candidates[i].ID == result.ID {
			candidate = &candidates[i]
			break
		}
	}
	if candidate == nil || candidate.Lifecycle != model.MemoryCandidate || candidate.Authority != model.AuthorityAgent {
		t.Fatalf("remembered candidate = %+v, want durable candidate/agent", candidate)
	}
	if stored, err := runtime.Store().GetMemoryV2(ctx, project.ID, result.ID); err != nil || stored.Lifecycle != model.MemoryCandidate {
		t.Fatalf("unapproved MCP candidate should persist only as candidate; record=%+v err=%v", stored, err)
	}
	recalled, err := srv.callTool(ctx, caller, "memory_recall", map[string]any{"project_id": project.ID, "query": "MCP candidate"})
	if err != nil {
		t.Fatal(err)
	}
	var recallResult struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(recalled), &recallResult); err != nil {
		t.Fatal(err)
	}
	for _, item := range recallResult.Results {
		if item.ID == result.ID {
			t.Fatalf("unapproved candidate unexpectedly appeared in memory_recall results: %s", recalled)
		}
	}

	// memory_status counts persisted project memory independently of author.
	fixture := *candidate
	fixture.ID = "MEM-MCP-STATUS-FIXTURE"
	fixture.Scope = string(model.ScopeTask)
	fixture.ScopeID = "TASK-MCP-STATUS"
	fixture.Source.Reference = "cli-operator"
	if err := runtime.Store().WriteMemoryV2(ctx, fixture); err != nil {
		t.Fatal(err)
	}
	statusJSON, err := srv.callTool(ctx, caller, "memory_status", map[string]any{"project_id": project.ID})
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Records       int    `json:"records"`
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
		t.Fatal(err)
	}
	all, err := runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if status.Records != len(all) || status.Records != 2 || status.SchemaVersion == "" {
		t.Fatalf("MCP status=%+v project records=%d, want candidate plus fixture and schema version", status, len(all))
	}
}

func TestMCPMemoryMutationToolsRequireTheirIdentifiers(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewServer(runtime)
	caller := auth.Principal{ID: "mcp-test", Capabilities: []string{string(auth.CapAll)}}
	if directCandidateRetention {
		if _, err := srv.callTool(ctx, caller, "memory_remember", map[string]any{"title": " ", "body": "body"}); !errors.Is(err, model.ErrInvalid) {
			t.Errorf("memory_remember with missing required value returned %v, want invalid argument", err)
		}
	} else if _, err := srv.callTool(ctx, caller, "memory_remember", map[string]any{"title": " ", "body": "body"}); err == nil {
		// Extreme refuses every standalone remember, valid or not.
		t.Error("standalone Marshal memory_remember with a blank title was accepted")
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{name: "memory_handoff_create", args: map[string]any{"task_id": " ", "target_role": "developer"}},
		{name: "memory_handoff_create", args: map[string]any{"task_id": "TASK-1", "target_role": " "}},
		{name: "memory_handoff_consume", args: map[string]any{"handoff_id": " "}},
	} {
		if _, err := srv.callTool(ctx, caller, tc.name, tc.args); !errors.Is(err, model.ErrInvalid) {
			t.Errorf("%s with missing required value returned %v, want invalid argument", tc.name, err)
		}
	}
}

func TestInsecureMemoryPrincipalGrantsHandoffCreate(t *testing.T) {
	// Local-only MCP callers (insecure mode) carry no auth capabilities, yet
	// memory_handoff_create must still compile and submit a typed handoff. The
	// handoff service authorizes on handoff.create, so memoryPrincipal grants it.
	principal := memoryPrincipal(auth.Principal{ID: "mcp-client"})
	found := false
	for _, cap := range principal.Role.Capabilities {
		if cap == string(auth.CapHandoffCreate) {
			found = true
		}
	}
	if !found {
		t.Fatalf("insecure MCP principal capabilities = %v, want handoff.create", principal.Role.Capabilities)
	}
	for _, a := range principal.Role.Authorities {
		if a == authz.AuthoritySourceWrite || a == authz.AuthorityPolicyAdmin {
			t.Fatalf("insecure MCP principal granted privileged authority %q", a)
		}
	}
}
