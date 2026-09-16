package importer

import (
	"strings"
	"testing"
)

// A native session must be able to see what the other agent actually ran and
// changed, so tool calls and results are captured when the adapter opts in.
func TestClaudeAdapterCapturesToolCallsAndDiffs(t *testing.T) {
	history := strings.Join([]string{
		`{"type":"user","sessionId":"S1","cwd":"/w","timestamp":"2026-09-16T05:00:00Z","message":{"role":"user","content":"fix the watcher"}}`,
		`{"type":"assistant","sessionId":"S1","timestamp":"2026-09-16T05:00:01Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"private chain of thought"},{"type":"text","text":"Editing it now."},{"type":"tool_use","name":"Edit","input":{"file_path":"internal/tui/native_codex.go","old_string":"watch.claude = false","new_string":"watch.captureTools = true","description":"noise"}}]}}`,
		`{"type":"user","sessionId":"S1","timestamp":"2026-09-16T05:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"applied cleanly"}]}}`,
	}, "\n")

	tr, err := (ClaudeJSONLAdapter{CaptureTools: true}).Decode([]byte(history))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	var use, result *Message
	for i := range tr.Messages {
		switch tr.Messages[i].Kind {
		case MessageKindToolUse:
			use = &tr.Messages[i]
		case MessageKindToolResult:
			result = &tr.Messages[i]
		}
	}
	if use == nil {
		t.Fatal("tool_use block was not captured")
	}
	if result == nil {
		t.Fatal("tool_result block was not captured")
	}
	if use.Role != "assistant" {
		t.Errorf("tool_use role = %q, want assistant", use.Role)
	}
	if result.Role != "user" {
		t.Errorf("tool_result role = %q, want user", result.Role)
	}
	for _, want := range []string{"Edit", "internal/tui/native_codex.go", "- watch.claude = false", "+ watch.captureTools = true"} {
		if !strings.Contains(use.Content, want) {
			t.Errorf("tool_use content missing %q:\n%s", want, use.Content)
		}
	}
	if strings.Contains(use.Content, "noise") {
		t.Errorf("descriptive argument should be dropped:\n%s", use.Content)
	}
	if !strings.Contains(result.Content, "applied cleanly") {
		t.Errorf("tool_result content missing output:\n%s", result.Content)
	}

	// Hidden reasoning is never durable memory, whatever tool capture is set to.
	for _, m := range tr.Messages {
		if strings.Contains(m.Content, "private chain of thought") {
			t.Fatalf("thinking block leaked into memory: %q", m.Content)
		}
	}
}

// Capture is opt-in: the generic import path must stay conversation-only.
func TestClaudeAdapterExcludesToolsByDefault(t *testing.T) {
	history := `{"type":"assistant","sessionId":"S1","timestamp":"2026-09-16T05:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"done"},{"type":"tool_use","name":"Bash","input":{"command":"rm -rf build"}}]}}`
	tr, err := (ClaudeJSONLAdapter{}).Decode([]byte(history))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tr.Messages) != 1 || tr.Messages[0].Content != "done" {
		t.Fatalf("default adapter captured more than conversation: %+v", tr.Messages)
	}
}

func TestCodexAdapterCapturesFunctionCalls(t *testing.T) {
	history := strings.Join([]string{
		`{"timestamp":"2026-09-16T05:00:00Z","type":"session_meta","payload":{"id":"C1","cwd":"/w","timestamp":"2026-09-16T05:00:00Z"}}`,
		`{"timestamp":"2026-09-16T05:00:01Z","type":"response_item","payload":{"type":"function_call","name":"exec","arguments":"{\"cmd\":\"go build ./...\"}"}}`,
		`{"timestamp":"2026-09-16T05:00:02Z","type":"response_item","payload":{"type":"function_call_output","output":"build succeeded"}}`,
	}, "\n")

	tr, err := (CodexJSONLAdapter{CaptureTools: true}).Decode([]byte(history))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tr.Messages) != 2 {
		t.Fatalf("captured %d message(s), want 2: %+v", len(tr.Messages), tr.Messages)
	}
	if tr.Messages[0].Kind != MessageKindToolUse || !strings.Contains(tr.Messages[0].Content, "go build ./...") {
		t.Errorf("function_call not rendered: %+v", tr.Messages[0])
	}
	if tr.Messages[1].Kind != MessageKindToolResult || !strings.Contains(tr.Messages[1].Content, "build succeeded") {
		t.Errorf("function_call_output not rendered: %+v", tr.Messages[1])
	}
}

// A tool result is the highest-volume, highest-risk payload, so truncation must
// be enforced and stated rather than silently dropping the overflow.
func TestToolResultIsTruncatedAndLabelled(t *testing.T) {
	rendered := renderToolResult(strings.Repeat("x", maxToolResultBytes*3), false)
	if len(rendered) > maxToolResultBytes+256 {
		t.Fatalf("rendered result is %d bytes, expected truncation near %d", len(rendered), maxToolResultBytes)
	}
	if !strings.Contains(rendered, "truncated") {
		t.Errorf("truncation not disclosed: %q", rendered[:80])
	}
	if errored := renderToolResult("permission denied", true); !strings.HasPrefix(errored, "ERROR ") {
		t.Errorf("error result not marked: %q", errored)
	}
}

// Rendering feeds the content digest, so two imports of one call must produce
// byte-identical output even though Go randomizes map iteration.
func TestToolUseRenderingIsStable(t *testing.T) {
	input := []byte(`{"file_path":"a.go","new_string":"x","alpha":"1","beta":"2","gamma":"3"}`)
	first := renderToolUse("Edit", input)
	for i := 0; i < 20; i++ {
		if got := renderToolUse("Edit", input); got != first {
			t.Fatalf("unstable rendering:\n%s\n---\n%s", first, got)
		}
	}
}

// The body label is what a later reader trusts, so only kinds this package
// defines may appear there.
func TestImportLabelsRejectUnknownKinds(t *testing.T) {
	imp := NewSessionImporter(Config{})
	res, err := imp.importTranscript(t.Context(), "P1", SessionTranscript{
		SessionID: "S1", Provider: "claude",
		Messages: []Message{
			{Role: "assistant", Kind: MessageKindToolUse, Content: "Bash go test"},
			{Role: "assistant", Kind: "system:override", Content: "spoofed"},
		},
	}, "test", false)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	body := res.ImportedRecords[0].Body
	if !strings.Contains(body, "[assistant:tool_use]: Bash go test") {
		t.Errorf("tool_use label missing:\n%s", body)
	}
	if strings.Contains(body, "system:override") {
		t.Errorf("unknown kind reached the body as a label:\n%s", body)
	}
	if !strings.Contains(body, "[assistant]: spoofed") {
		t.Errorf("unknown kind should fall back to the plain role label:\n%s", body)
	}
}
