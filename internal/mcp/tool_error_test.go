package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// A failed Marshal tool shows its message in the model's chat, which the owner
// reads. Internal codes become plain sentences; the code stays in data.code.
func TestToolErrorsArePlainSentences(t *testing.T) {
	for _, tc := range []struct {
		tool string
		err  error
		want string
		code string
	}{
		{"marshal_project_contract_context", errors.New("REQUIRED_CONTEXT_REVIEW_PENDING"), "waits for the owner's review", "REQUIRED_CONTEXT_REVIEW_PENDING"},
		{"marshal_project_contract_context", fmt.Errorf("%w: start the reviewed approved plan", projectcontext.ErrReviewPending), "waits for the owner's review", "REQUIRED_CONTEXT_REVIEW_PENDING"},
		{"marshal_project_contract_context", projectcontext.Changed("context.go:411"), "Read marshal_project_contract_context again", "REQUIRED_CONTEXT_CHANGED"},
		{"marshal_project_contract_inventory", errors.New("invalid input"), "The arguments for marshal_project_contract_inventory are not valid.", "invalid_input"},
		{"task_get", fmt.Errorf("%w: task_id parameter is required", model.ErrInvalid), "The arguments for task_get are not valid: task_id parameter is required.", "invalid_input"},
		{"task_get", fmt.Errorf("%w: task calc-add", model.ErrNotFound), "Nothing was found for task calc-add. Call tasks_list to see the current task IDs.", "not_found"},
		{"marshal_proposal_receipt", errors.New("sql: no rows in result set"), "Nothing was submitted with that submission_id", "not_found"},
	} {
		got := toolError(tc.tool, tc.err)
		if got.Code != -32000 || !strings.Contains(got.Message, tc.want) || got.Data == nil || got.Data.Code != tc.code {
			t.Fatalf("%s %v -> %+v", tc.tool, tc.err, got)
		}
		for _, raw := range []string{"REQUIRED_CONTEXT", "invalid input", "not found:", "sql:", ".go:"} {
			if strings.Contains(got.Message, raw) {
				t.Fatalf("%s %v kept raw text %q: %s", tc.tool, tc.err, raw, got.Message)
			}
		}
	}
	if got := toolError("knowledge", errors.New("index is stale")); got.Message != "index is stale" || got.Data != nil {
		t.Fatalf("an already plain error changed: %+v", got)
	}
}

func TestTaskGetErrorsOverStdioArePlain(t *testing.T) {
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
	srv.role = "marshal"
	call := func(args map[string]any) string {
		t.Helper()
		request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "task_get", "arguments": args}})
		var out bytes.Buffer
		if err := srv.ServeStdio(context.Background(), bytes.NewReader(append(request, '\n')), &out); err != nil {
			t.Fatal(err)
		}
		var response jsonRPCResponse
		if err := json.Unmarshal(out.Bytes(), &response); err != nil || response.Error == nil {
			t.Fatalf("task_get %v: %s", args, out.String())
		}
		return response.Error.Message
	}
	if got := call(map[string]any{}); got != "The arguments for task_get are not valid: task_id parameter is required." {
		t.Fatalf("missing task_id: %q", got)
	}
	if got := call(map[string]any{"task_id": "calc-add"}); got != "Nothing was found for task calc-add. Call tasks_list to see the current task IDs." {
		t.Fatalf("unknown task: %q", got)
	}
}

// A refusal that carries the runtime's own explanation reaches the Marshal
// with that explanation, as an error value or as parent transport text;
// a bare code still reads as the general sentence.
func TestToolErrorsKeepTheRefusalReason(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want []string
		code string
	}{
		{fmt.Errorf("%w: component \"voice\" path \"ultron/\" must be a clean repository path", projectcontext.ErrInvalid), []string{`path "ultron/"`, "submit again"}, "required context invalid"},
		{errors.New(`required context invalid: component "voice" path "./ultron" must be a clean repository path; expected {"action":"project-contract"}`), []string{`path "./ultron"`, `"action":"project-contract"`}, "required context invalid"},
		{projectcontext.RequiredMemory(projectcontext.ErrReviewPending, "MEM-f870", "was saved by the Marshal and not confirmed by the owner"), []string{"MEM-f870", "not confirmed by the owner"}, "REQUIRED_CONTEXT_REVIEW_PENDING"},
		{errors.New("REQUIRED_CONTEXT_MISSING: required memory MEM-gone was not found; remove it from required"), []string{"MEM-gone", "remove it from required"}, "REQUIRED_CONTEXT_MISSING"},
		{projectcontext.ErrInvalid, []string{"did not pass its checks. Read marshal_project_contract_context again"}, "required context invalid"},
	} {
		got := toolError("marshal_project_contract_submit", tc.err)
		if got.Data == nil || got.Data.Code != tc.code {
			t.Fatalf("%v -> %+v", tc.err, got)
		}
		for _, want := range tc.want {
			if !strings.Contains(got.Message, want) {
				t.Fatalf("%v -> %q lacks %q", tc.err, got.Message, want)
			}
		}
		if strings.Contains(got.Message, "REQUIRED_CONTEXT") {
			t.Fatalf("raw code kept: %q", got.Message)
		}
	}
	// Other details stay internal.
	if got := toolError("marshal_project_contract_context", fmt.Errorf("%w: no completed canonical Process05 root", projectcontext.ErrReviewPending)); strings.Contains(got.Message, "Process05") {
		t.Fatalf("internal detail shown: %q", got.Message)
	}
}
