package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The Board keeps the final prose of the Marshal's last turn: never a tool
// call or a tool's output, never a proposal line, never a secret.
func TestNoteMarshalSaid(t *testing.T) {
	w := &Workspace{}
	var b vm.Board
	w.collectV2MarshalSays(&b)
	if b.MarshalSays != (vm.MarshalSays{}) {
		t.Fatalf("nothing said yet: %+v", b.MarshalSays)
	}
	at := time.Date(2026, 11, 20, 19, 27, 0, 0, time.UTC)
	key := "sk-ant-api03-" + strings.Repeat("Zx9Qw3Er7Ty", 6)
	w.noteMarshalSaid(importer.SessionTranscript{Messages: []importer.Message{
		{Role: "user", Content: "build the review fixes"},
		{Role: "assistant", Content: "Reading the plan first."},
		{Role: "assistant", Kind: importer.MessageKindToolUse, Content: `{"cmd":"cat secrets.env"}`},
		{Role: "user", Kind: importer.MessageKindToolResult, Content: "TOKEN=" + key},
		{Role: "assistant", Timestamp: at, Content: "MARSHAL_PROPOSAL {\"kind\":\"retry\"}\nThe key " + key + " is set. Shall I start?"},
		{Role: "assistant", Kind: importer.MessageKindToolUse, Content: "tool call after the prose"},
	}})
	w.collectV2MarshalSays(&b)
	m := b.MarshalSays
	if !m.At.Equal(at) || m.Answered || !strings.HasPrefix(m.Text, "The key ") || !strings.HasSuffix(m.Text, "Shall I start?") {
		t.Fatalf("last turn: %+v", m)
	}
	for _, bad := range []string{key, "Zx9Qw3Er7Ty", "MARSHAL_PROPOSAL", "cat secrets", "tool call"} {
		if strings.Contains(m.Text, bad) {
			t.Errorf("kept %q: %q", bad, m.Text)
		}
	}
	// A batch with only tool traffic keeps what was said; the owner's
	// reply marks it answered.
	w.noteMarshalSaid(importer.SessionTranscript{Messages: []importer.Message{{Role: "user", Kind: importer.MessageKindToolResult, Content: "ok"}}})
	w.collectV2MarshalSays(&b)
	if b.MarshalSays.Answered || b.MarshalSays.Text != m.Text {
		t.Fatalf("tool output changed the turn: %+v", b.MarshalSays)
	}
	w.noteMarshalSaid(importer.SessionTranscript{Messages: []importer.Message{{Role: "user", Content: "yes, start"}}})
	w.collectV2MarshalSays(&b)
	if !b.MarshalSays.Answered || b.MarshalSays.Text != m.Text {
		t.Fatalf("answered: %+v", b.MarshalSays)
	}
}
