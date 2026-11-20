package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLines(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadSessionClaude(t *testing.T) {
	path := writeLines(t,
		`{"type":"assistant","effort":"low","message":{"model":"claude-sonnet-5-5","usage":{"input_tokens":1}}}`,
		`{"type":"assistant","effort":"medium","message":{"model":"claude-opus-5-5","usage":{"input_tokens":2,"cache_creation_input_tokens":816,"cache_read_input_tokens":69069,"output_tokens":113}}}`,
		`{"type":"assistant","isSidechain":true,"effort":"low","message":{"model":"claude-haiku-5","usage":{"input_tokens":9}}}`,
		`{"type":"user","message":{"content":"the word \"assistant\" here"}}`,
	)
	s, ok := ReadSession("claude", path)
	if !ok || s.Model != "claude-opus-5-5" || s.Effort != "medium" || s.ContextUsed != 70000 || s.ContextWindow != 0 || s.AsOf.IsZero() {
		t.Fatalf("session %+v ok %v", s, ok)
	}
}

func TestReadSessionCodex(t *testing.T) {
	path := writeLines(t,
		`{"type":"turn_context","payload":{"model":"gpt-6.0","effort":"low"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":1000},"model_context_window":200000}}}`,
		`{"type":"turn_context","payload":{"model":"gpt-6.1-sol","effort":"high"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":15970},"model_context_window":258400}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{}}}`,
	)
	s, ok := ReadSession("codex", path)
	if !ok || s.Model != "gpt-6.1-sol" || s.Effort != "high" || s.ContextUsed != 15970 || s.ContextWindow != 258400 {
		t.Fatalf("session %+v ok %v", s, ok)
	}
}

func TestReadSessionUnknown(t *testing.T) {
	if _, ok := ReadSession("claude", filepath.Join(t.TempDir(), "missing.jsonl")); ok {
		t.Fatal("a missing transcript read as a session")
	}
	if _, ok := ReadSession("agy", writeLines(t, `{"type":"assistant"}`)); ok {
		t.Fatal("a provider without a transcript reader read a session")
	}
	if _, ok := ReadSession("claude", writeLines(t, `{"type":"assistant","message":{"model":"<synthetic>"}}`)); ok {
		t.Fatal("a synthetic turn read as a model")
	}
}
