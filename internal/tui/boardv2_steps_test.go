package tui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshalsteps"
	"github.com/Zen1th53/marshal/internal/permission"
)

// The steps pane shows the Marshal's tool calls from the step log, so a
// Board that starts after the Marshal made them (a restart: the in-memory
// steps of the Board that served them are gone) still shows them, with the
// quiet clock from the last call, and the calls the Marshal's MCP child
// logged for the tools it serves itself show beside the broker's.
func TestStepsComeFromTheStepLog(t *testing.T) {
	path := marshalsteps.Path(filepath.Join(t.TempDir(), ".marshal"))
	t0 := time.Date(2026, 11, 20, 17, 0, 0, 0, time.UTC)
	var served marshalSteps
	served.bind(path)
	served.record("L1", "claude", "read", "architecture context page 1/2", t0)
	served.record("L1", "claude", "", "", t0.Add(time.Minute)) // a receipt
	// The MCP child appends its own line for a knowledge tool.
	if err := marshalsteps.Append(path, marshalsteps.Line{At: t0.Add(2 * time.Minute), Kind: "read", Text: "project search"}); err != nil {
		t.Fatal(err)
	}

	var restarted marshalSteps // a new Board process on the same project
	restarted.bind(path)
	items, last, provider := restarted.snapshot()
	got := stepsView(items, last, "", t0.Add(12*time.Minute))
	var lines []string
	for _, st := range got.Items {
		lines = append(lines, st.Kind+" "+st.Text)
	}
	want := []string{"read project search", "read architecture context page 1/2", "start session on claude"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("steps after restart\n got %q\nwant %q", lines, want)
	}
	if got.Quiet != 10*time.Minute || provider != "claude" {
		t.Fatalf("quiet %s provider %q", got.Quiet, provider)
	}

	// A later child line reaches a running Board on the monitor's refresh,
	// not on a frame.
	if err := marshalsteps.Append(path, marshalsteps.Line{At: t0.Add(3 * time.Minute), Kind: "read", Text: "related files"}); err != nil {
		t.Fatal(err)
	}
	if items, _, _ := restarted.snapshot(); items[0].Text != "project search" {
		t.Fatalf("a frame read the log: %+v", items[0])
	}
	restarted.refresh()
	if items, _, _ := restarted.snapshot(); items[0].Text != "related files" {
		t.Fatalf("refresh missed the child's line: %+v", items[0])
	}
	// The Board's own call is in the log and in its steps at once, once.
	restarted.record("L2", "claude", "submit", "architecture rev 2", t0.Add(4*time.Minute))
	items, _, _ = restarted.snapshot()
	if items[0].Text != "architecture rev 2" || items[1].Text != "session on claude" || items[2].Text != "related files" {
		t.Fatalf("own call: %+v", items[:3])
	}
}

// Without a state directory the steps are kept in memory.
func TestStepsWithoutStepLog(t *testing.T) {
	var s marshalSteps
	s.record("L1", "codex", "read", "accepted architecture", time.Now())
	items, last, provider := s.snapshot()
	if len(items) != 2 || items[0].Text != "accepted architecture" || last.IsZero() || provider != "codex" {
		t.Fatalf("memory steps: %+v %v %q", items, last, provider)
	}
}

// Each broker call says what it was in a few plain words: the context page
// it read, the revision it submitted, the request a decision answered as the
// Board sent it. Submitted text never shows.
func TestBrokerStepWords(t *testing.T) {
	cases := []struct {
		method   string
		raw, out string
		want     string
	}{
		{"project_contract_context", `{}`, `{"read_id":"r","page":1,"pages":2,"context":"secret prompt"}`, "read architecture context page 1/2"},
		{"project_contract_context", `{}`, `{"page":1,"pages":1}`, "read architecture context"},
		{"project_contract_context", `{}`, ``, "read architecture context"},
		{"project_contract_submit", `{"submission_id":"s","proposal":{"action":"project-contract","contract":{"revision":1,"purpose":"secret prompt"}}}`, ``, "submit architecture rev 1"},
		{"memory_submit", `{"title":"secret prompt","body":"x"}`, ``, "submit memory note"},
		{"submit", `{"submission_id":"s","proposal":{"action":"decide","id":"q1","allow":true,"reason":"secret prompt"}}`, ``, "decide a request → allow"},
		{"submit", `{"submission_id":"s","proposal":{"action":"retry","task_id":"T03","fresh_session":true}}`, ``, "start retry a task T03"},
		{"project_contract_receipt", `{}`, ``, " "},
	}
	for _, c := range cases {
		var out json.RawMessage
		if c.out != "" {
			out = json.RawMessage(c.out)
		}
		kind, text := stepOf(c.method, json.RawMessage(c.raw), out)
		if got := kind + " " + text; got != c.want {
			t.Errorf("%s %s: %q, want %q", c.method, c.raw, got, c.want)
		}
		if strings.Contains(text, "secret") {
			t.Errorf("%s: step %q carries submitted text", c.method, text)
		}
	}

	w := emptyBoardWorkspace(t)
	w.chatRequestSent("q1", permission.Request{Kind: "memory", Object: "MEM-IMPORT-7"}, time.Now())
	w.noteBrokerStep("L1", "claude", "submit", json.RawMessage(`{"submission_id":"s","proposal":{"action":"decide","id":"q1","allow":true,"reason":"r"}}`), nil, nil)
	w.noteBrokerStep("L1", "claude", "project_contract_conformance", json.RawMessage(`{}`), nil, errTestRefused)
	items, _, _ := w.marshalSteps.snapshot()
	if len(items) != 3 || items[1].Kind != "decide" || items[1].Text != "memory import → allow" || items[0].Text != "architecture conformance · refused" {
		t.Fatalf("named decision: %+v", items)
	}
}

var errTestRefused = errTestString("refused")

type errTestString string

func (e errTestString) Error() string { return string(e) }
