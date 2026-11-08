//go:build !governed

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func recallRuntimeFixture(t *testing.T) *app.Runtime {
	t.Helper()
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func seedRecallMemory(t *testing.T, runtime *app.Runtime, id, body string, options ...func(*model.MemoryRecordV2)) model.MemoryRecordV2 {
	t.Helper()
	project, err := runtime.Store().Project(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := model.MemoryRecordV2{ID: id, ProjectID: project.ID, Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryDurable, Authority: model.AuthorityVerified, Title: "architecture 東京 <evidence>", Body: body, Scope: string(model.ScopeProject), ScopeID: project.ID, Source: model.MemorySource{Kind: "runtime_service", Reference: "source-labelled architecture study", AgentID: "claude"}, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now}
	for _, option := range options {
		option(&record)
	}
	if err := runtime.Store().WriteMemoryV2(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	return record
}

func recallWire(t *testing.T, server *Server, id any, args map[string]any, http bool) ([]byte, compactRecallResponse, *rpcErr) {
	t.Helper()
	request, err := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: json.RawMessage(mustRecallJSON(t, map[string]any{"name": "memory_recall", "arguments": args}))})
	if err != nil {
		t.Fatal(err)
	}
	var wire []byte
	if http {
		writer := httptest.NewRecorder()
		server.Handler().ServeHTTP(writer, httptest.NewRequest("POST", "/", bytes.NewReader(request)))
		wire = writer.Body.Bytes()
	} else {
		var output bytes.Buffer
		if err := server.ServeStdio(t.Context(), bytes.NewReader(append(request, '\n')), &output); err != nil {
			t.Fatal(err)
		}
		wire = output.Bytes()
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
		t.Fatalf("invalid response: %v %s", err, wire)
	}
	if envelope.Error != nil {
		return wire, compactRecallResponse{}, envelope.Error
	}
	if len(envelope.Result.Content) != 1 {
		t.Fatalf("response has no content: %s", wire)
	}
	var result compactRecallResponse
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &result); err != nil {
		t.Fatal(err)
	}
	return wire, result, nil
}

func mustRecallJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestExtremeRecallCompleteWireBudgetAndUsefulSnippets(t *testing.T) {
	runtime := recallRuntimeFixture(t)
	body := strings.Repeat("architecture 東京 β <tool> & \"proof\" \\ next\r\n", 450)
	for i := 0; i < 7; i++ {
		seedRecallMemory(t, runtime, fmt.Sprintf("MEM-WIRE-%d", i), body)
	}
	server := NewExtremeServer(runtime, "marshal", "")
	for _, http := range []bool{false, true} {
		for _, budget := range []int{1024, 2048, 4096, 8192} {
			t.Run(fmt.Sprintf("http=%v/budget=%d", http, budget), func(t *testing.T) {
				wire, response, rpcError := recallWire(t, server, "req東京\"<&>\\", map[string]any{"query": "architecture", "max_bytes": budget, "max_records": 3}, http)
				if rpcError != nil {
					t.Fatal(rpcError.Message)
				}
				if len(wire) > budget || response.Bytes != len(wire) || !utf8.Valid(wire) {
					t.Fatalf("wire=%d report=%d budget=%d utf8=%v", len(wire), response.Bytes, budget, utf8.Valid(wire))
				}
				if len(response.Results) == 0 || len(response.Results) > 3 || !response.Truncated || response.Receipt.ID == "" {
					t.Fatalf("missing bounded useful response: %+v", response)
				}
				if response.Results[0].Text == "" || response.Results[0].Authority != string(model.AuthorityVerified) || !utf8.ValidString(response.Results[0].Text) {
					t.Fatalf("not a source-cited useful UTF-8 snippet: %+v", response.Results[0])
				}
				if response.Results[0].Source.Kind != "runtime_service" || response.Results[0].Source.Reference == "" || response.Results[0].Source.ObservedAt == "" {
					t.Fatalf("authorized provenance omitted: %+v", response.Results[0])
				}
				if strings.Contains(string(wire), "marshal_memory_context") || len(wire) >= len(body) {
					t.Fatal("raw large context was duplicated into compact output")
				}
			})
		}
	}
}

func TestExtremeRecallAuthorizedTaskScopeAndSnapshotProvenance(t *testing.T) {
	runtime := recallRuntimeFixture(t)
	project, err := runtime.Store().Project(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Store().RegisterAgent(t.Context(), model.Agent{ID: "marshal", ProjectID: project.ID, DisplayName: "Marshal", Role: model.RoleDeveloper, Status: model.AgentRegistered}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Store().PutRoleBinding(t.Context(), authz.RoleBinding{ID: "memory-task", PrincipalID: "marshal", Role: "task-member", ScopeID: "TASK-context", BoundBy: "test-operator", BoundAt: time.Now().UTC(), PolicyDigest: "sha256:" + strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	record := seedRecallMemory(t, runtime, "MEM-TASK-SCOPED", "architecture task-scoped source context", func(record *model.MemoryRecordV2) {
		record.Scope, record.ScopeID = string(model.ScopeTask), "TASK-context"
		record.HeadCommit = "observed-source-commit"
	})
	server := NewExtremeServer(runtime, "marshal", "")
	_, response, rpcError := recallWire(t, server, "scope", map[string]any{"query": record.ID, "detail": true, "task_id": "TASK-context", "allowed_scope_ids": []string{"TASK-context"}, "current_head": "observed-source-commit", "current_branch": "main"}, false)
	if rpcError != nil || len(response.Results) != 1 || response.Results[0].Text != record.Body || response.Receipt.TaskID != "TASK-context" || response.Results[0].Source.HeadCommit != "observed-source-commit" {
		t.Fatalf("authorized task/source snapshot not forwarded: %+v %v", response, rpcError)
	}
}

func TestExtremeRecallDetailPagesUnicodeAndCarriageReturn(t *testing.T) {
	runtime := recallRuntimeFixture(t)
	body := strings.Repeat("東京 β <tool>&\"proof\" \\ \r\n", 200)
	record := seedRecallMemory(t, runtime, "MEM-DETAIL", body)
	server := NewExtremeServer(runtime, "marshal", "")
	var assembled strings.Builder
	offset := 0
	for page := 0; page < 100; page++ {
		wire, response, rpcError := recallWire(t, server, 12, map[string]any{"query": record.ID, "detail": true, "offset": offset, "max_bytes": 2048}, false)
		if rpcError != nil {
			t.Fatal(rpcError.Message)
		}
		if len(wire) > 2048 || response.Bytes != len(wire) || len(response.Results) != 1 {
			t.Fatalf("invalid detail page: %+v bytes=%d", response, len(wire))
		}
		item := response.Results[0]
		if item.BodyOffset != offset || item.Text == "" || !utf8.ValidString(item.Text) || item.BodyBytes != len(body) {
			t.Fatalf("invalid detail boundary: %+v", item)
		}
		assembled.WriteString(item.Text)
		if item.NextOffset == 0 {
			if assembled.String() != body {
				t.Fatalf("detail pages changed authorized body: got %d want %d", assembled.Len(), len(body))
			}
			return
		}
		if item.NextOffset != offset+len(item.Text) {
			t.Fatal("detail cursor skipped source bytes")
		}
		offset = item.NextOffset
	}
	t.Fatal("detail pagination made no bounded progress")
}

func TestExtremeRecallRejectsInvalidBudgetsAndHugeResponseID(t *testing.T) {
	runtime := recallRuntimeFixture(t)
	server := NewExtremeServer(runtime, "marshal", "")
	for _, args := range []map[string]any{
		{"query": "architecture", "max_bytes": 1},
		{"query": "architecture", "max_bytes": 65537},
		{"query": "architecture", "max_bytes": 1024.5},
		{"query": "architecture", "max_bytes": "1024"},
		{"query": "architecture", "max_records": 0},
		{"query": "architecture", "max_records": 21},
		{"query": "architecture", "detail": true},
		{"query": "MEM-ID", "detail": "true"},
		{"query": "architecture", "offset": 1},
		{"query": "architecture", "session_id": "other-session"},
	} {
		if _, err := server.callTool(t.Context(), server.defaultCaller(), "memory_recall", args); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("invalid argument accepted: %v => %v", args, err)
		}
	}
	_, _, rpcError := recallWire(t, server, strings.Repeat("<&>東京", 1024), map[string]any{"query": "architecture", "max_bytes": 1024}, false)
	if rpcError == nil || !strings.Contains(rpcError.Message, "cannot fit") {
		t.Fatal("huge request ID was represented as a successful bounded response")
	}
}

func TestExtremeRecallScopeHistoryAndFreshnessCannotBeBypassed(t *testing.T) {
	runtime := recallRuntimeFixture(t)
	record := seedRecallMemory(t, runtime, "MEM-ALLOWED", "source-labelled project architecture")
	imported := seedRecallMemory(t, runtime, "MEM-IMPORT-HIDDEN", "architecture private raw conversation", func(record *model.MemoryRecordV2) { record.Source.Kind = "external" })
	acl := seedRecallMemory(t, runtime, "MEM-OTHER-ACL", "architecture confidential peer record", func(record *model.MemoryRecordV2) { record.ACLScope = "other-reader" })
	stale := seedRecallMemory(t, runtime, "MEM-STALE", "architecture old runtime outcome", func(record *model.MemoryRecordV2) {
		record.HeadCommit = "old-commit"
		record.Source.Kind = "runtime_outcome"
		record.Kind = model.MemoryKindEpisodic
	})
	marshal := NewExtremeServer(runtime, "marshal", "")
	_, response, rpcError := recallWire(t, marshal, "history", map[string]any{"query": imported.ID, "detail": true}, false)
	if rpcError != nil || len(response.Results) != 1 || response.Results[0].Text != imported.Body {
		t.Fatalf("Marshal authorized history changed: %+v %v", response, rpcError)
	}
	worker := NewExtremeServer(runtime, "worker", "TASK-own")
	for _, id := range []string{imported.ID, acl.ID} {
		wire, response, rpcError := recallWire(t, worker, 13, map[string]any{"query": id, "detail": true}, false)
		if rpcError != nil || len(response.Results) != 0 || strings.Contains(string(wire), id) {
			t.Fatalf("worker detail leaked forbidden history/ACL: %+v %v %s", response, rpcError, wire)
		}
	}
	_, response, rpcError = recallWire(t, worker, 14, map[string]any{"query": record.ID, "detail": true}, false)
	if rpcError != nil || len(response.Results) != 1 || response.Results[0].Text != record.Body {
		t.Fatalf("legitimate project summary was denied: %+v %v", response, rpcError)
	}
	for _, args := range []map[string]any{
		{"query": "architecture", "project_id": "foreign-project"},
		{"query": "architecture", "task_id": "TASK-other"},
		{"query": "architecture", "allowed_scope_ids": []string{"session-peer"}},
		{"query": "architecture", "current_branch": "peer-branch", "allowed_scope_ids": []string{"peer-branch"}},
	} {
		if _, err := worker.callTool(context.Background(), worker.defaultCaller(), "memory_recall", args); !errors.Is(err, model.ErrPolicyDenied) {
			t.Fatalf("worker scope widened: %v => %v", args, err)
		}
	}
	if _, err := marshal.callTool(t.Context(), marshal.defaultCaller(), "memory_recall", map[string]any{"query": "architecture", "task_id": "TASK-forged", "current_branch": "TASK-forged"}); err == nil {
		t.Fatal("caller branch facts bypassed active task-memory authorization")
	}
	_, response, rpcError = recallWire(t, marshal, 15, map[string]any{"query": stale.ID, "detail": true, "current_head": "new-commit", "current_branch": "main"}, false)
	if rpcError != nil || len(response.Results) != 0 || response.Receipt.CurrentHead != "new-commit" || response.Receipt.Branch != "main" {
		t.Fatalf("freshness facts ignored: %+v %v", response, rpcError)
	}
}

// Detail paging intentionally retains Recall's existing canonical-context limit.
// It does not turn an exact ID into an unbounded raw-record export.
func TestExtremeRecallDetailRetainsCanonicalContextLimit(t *testing.T) {
	runtime := recallRuntimeFixture(t)
	server := NewExtremeServer(runtime, "marshal", "")
	large := seedRecallMemory(t, runtime, "MEM-LARGE-DETAIL", strings.Repeat("large authorized architecture note 東京\n", 3000))
	wire, response, rpcError := recallWire(t, server, "large", map[string]any{"query": large.ID, "detail": true, "max_bytes": 2048}, false)
	if rpcError != nil || len(response.Results) != 0 || !response.Truncated || response.Remaining != 1 || len(wire) > 2048 {
		t.Fatalf("canonical context limit was bypassed or hidden: %+v %v", response, rpcError)
	}
	small := seedRecallMemory(t, runtime, "MEM-UTF8-OFFSET", "東京 architecture")
	if _, err := server.callTool(t.Context(), server.defaultCaller(), "memory_recall", map[string]any{"query": small.ID, "detail": true, "offset": 1}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("offset inside a UTF-8 rune accepted: %v", err)
	}
}
