package importer

import (
	"strings"
	"testing"
	"time"
)

// wire builds protobuf messages for fixtures. The fields mirror the ones the
// adapter reads from real agy conversations; the content is synthetic.
type wire []byte

func (w wire) varint(field int, value uint64) wire {
	w = appendVarint(w, uint64(field)<<3|0)
	return appendVarint(w, value)
}

func (w wire) bytes(field int, value []byte) wire {
	w = appendVarint(w, uint64(field)<<3|2)
	w = appendVarint(w, uint64(len(value)))
	return append(w, value...)
}

func (w wire) str(field int, value string) wire { return w.bytes(field, []byte(value)) }

func appendVarint(b []byte, value uint64) []byte {
	for value >= 0x80 {
		b = append(b, byte(value)|0x80)
		value >>= 7
	}
	return append(b, byte(value))
}

func stepMetadata(at time.Time) []byte {
	created := wire{}.varint(1, uint64(at.Unix())).varint(2, uint64(at.Nanosecond()))
	return wire{}.bytes(1, created)
}

func userStep(index int, at time.Time, text string) AntigravityStep {
	input := wire{}.str(2, text).str(3, "\n"+text)
	return AntigravityStep{Index: index, Type: antigravityStepUserInput, Metadata: stepMetadata(at), Payload: wire{}.bytes(19, input)}
}

func plannerStep(index int, at time.Time, visible, reasoning string, calls ...wire) AntigravityStep {
	response := wire{}
	if visible != "" {
		response = response.str(1, visible)
	}
	if reasoning != "" {
		response = response.str(3, reasoning)
	}
	for _, call := range calls {
		response = response.bytes(7, call)
	}
	if visible != "" {
		response = response.str(8, visible)
	}
	return AntigravityStep{Index: index, Type: antigravityStepPlannerResponse, Metadata: stepMetadata(at), Payload: wire{}.bytes(20, response)}
}

func toolCall(id, name, args string) wire {
	return wire{}.str(1, id).str(2, name).str(3, args)
}

func toolResultStep(index int, at time.Time, call wire, output, failure string) AntigravityStep {
	payload := wire{}.bytes(5, wire{}.bytes(4, call))
	if output != "" {
		payload = payload.bytes(140, wire{}.bytes(2, wire{}.str(1, output)))
	}
	if failure != "" {
		payload = payload.bytes(31, wire{}.str(2, failure))
	}
	return AntigravityStep{Index: index, Type: 132, Metadata: stepMetadata(at), Payload: payload}
}

func TestAntigravityConversationKeepsVisibleTextAndTools(t *testing.T) {
	base := time.Date(2026, 9, 18, 6, 50, 8, 973334016, time.UTC)
	call := toolCall("call_1", "run_command", `{"CommandLine":"go test ./...","Cwd":"/repo"}`)
	steps := []AntigravityStep{
		// Deliberately out of order: the table is read by index.
		plannerStep(3, base.Add(7*time.Second), "All tests pass.", ""),
		userStep(0, base, "Run the tests and tell me the result."),
		plannerStep(1, base.Add(time.Second), "", "PRIVATE-REASONING must never be stored", call),
		toolResultStep(2, base.Add(6*time.Second), call, "The command exited with code 0.\nOutput:\nok  example.com/repo", ""),
	}

	tr, err := DecodeAntigravityConversation("conv-1", steps, true)
	if err != nil {
		t.Fatal(err)
	}
	if tr.SessionID != "conv-1" || tr.Provider != "antigravity" {
		t.Fatalf("unexpected transcript identity: %+v", tr)
	}
	if !tr.Timestamp.Equal(base) {
		t.Fatalf("transcript time %s, want the first step's %s", tr.Timestamp, base)
	}

	var rendered []string
	for _, m := range tr.Messages {
		rendered = append(rendered, m.Role+"|"+m.Kind+"|"+m.Content)
		if strings.Contains(m.Content, "PRIVATE-REASONING") {
			t.Fatalf("the model's reasoning was stored: %q", m.Content)
		}
	}
	if len(tr.Messages) != 4 {
		t.Fatalf("expected user, tool call, tool result and answer, got %d:\n%s", len(tr.Messages), strings.Join(rendered, "\n"))
	}
	want := []struct{ role, kind, contains string }{
		{"user", MessageKindText, "Run the tests"},
		{"assistant", MessageKindToolUse, "go test ./..."},
		{"user", MessageKindToolResult, "ok  example.com/repo"},
		{"assistant", MessageKindText, "All tests pass."},
	}
	for i, w := range want {
		m := tr.Messages[i]
		if m.Role != w.role || m.Kind != w.kind || !strings.Contains(m.Content, w.contains) {
			t.Fatalf("message %d = %s|%s|%q, want %s|%s containing %q", i, m.Role, m.Kind, m.Content, w.role, w.kind, w.contains)
		}
	}
	if !tr.Messages[0].Timestamp.Equal(base) {
		t.Fatalf("message time %s, want the step's own %s", tr.Messages[0].Timestamp, base)
	}
}

// Without tool capture only what was said is kept, and a failed tool call is
// recorded as the failure it was.
func TestAntigravityConversationToolCaptureAndFailures(t *testing.T) {
	base := time.Date(2026, 9, 18, 6, 49, 4, 0, time.UTC)
	call := toolCall("call_2", "run_command", `{"CommandLine":"rm -rf build"}`)
	steps := []AntigravityStep{
		userStep(0, base, "Clean the build directory."),
		plannerStep(1, base.Add(time.Second), "", "", call),
		toolResultStep(2, base.Add(2*time.Second), call, "", "permission check failed: user denied permission to run command"),
	}

	quiet, err := DecodeAntigravityConversation("conv-2", steps, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(quiet.Messages) != 1 || quiet.Messages[0].Kind != MessageKindText {
		t.Fatalf("with tool capture off only conversation text is kept, got %+v", quiet.Messages)
	}

	full, err := DecodeAntigravityConversation("conv-2", steps, true)
	if err != nil {
		t.Fatal(err)
	}
	last := full.Messages[len(full.Messages)-1]
	if last.Kind != MessageKindToolResult || !strings.Contains(last.Content, "ERROR") || !strings.Contains(last.Content, "denied") {
		t.Fatalf("a denied call should be recorded as a failure, got %q", last.Content)
	}
}

// A step whose payload cannot be read is skipped; the rest of the conversation
// is still imported. Repeated decoding yields identical times, because a
// record's identity is keyed on them.
func TestAntigravityConversationIsRobustAndStable(t *testing.T) {
	base := time.Date(2026, 9, 18, 6, 49, 38, 55322961, time.UTC)
	steps := []AntigravityStep{
		userStep(0, base, "Say hello."),
		{Index: 1, Type: antigravityStepPlannerResponse, Payload: []byte{0x0a, 0xff, 0xff}},
		plannerStep(2, base.Add(time.Second), "Hello.", ""),
	}
	first, err := DecodeAntigravityConversation("conv-3", steps, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Messages) != 2 {
		t.Fatalf("a corrupt step should be skipped without losing the rest, got %d messages", len(first.Messages))
	}
	second, err := DecodeAntigravityConversation("conv-3", steps, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first.Messages {
		if !first.Messages[i].Timestamp.Equal(second.Messages[i].Timestamp) {
			t.Fatal("decoding the same conversation twice produced different times")
		}
	}

	if _, err := DecodeAntigravityConversation("", steps, true); err == nil {
		t.Fatal("a conversation with no ID was accepted")
	}
}
