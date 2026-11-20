package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// Every command failure becomes one plain sentence: what failed and the one
// thing to do, never the raw Go error chain.
func TestPlainCommandErrorTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"not found", fmt.Errorf("read runs: %w", model.ErrNotFound), "/approve didn't work: nothing by that name was found."},
		{"goal not found", fmt.Errorf("load goal: %w", model.ErrGoalNotFound), "/approve didn't work: nothing by that name was found."},
		{"conflict", fmt.Errorf("apply: %w", model.ErrConflict), "/approve didn't work: something changed at the same moment. Try again."},
		{"goal conflict", fmt.Errorf("apply: %w", model.ErrGoalConflict), "/approve didn't work: something changed at the same moment. Try again."},
		{"approval required", fmt.Errorf("gate: %w", model.ErrApprovalRequired), "/approve needs your approval first. Look at the inbox on the Board."},
		{"policy denied", fmt.Errorf("check: %w", model.ErrPolicyDenied), "/approve isn't allowed here."},
		{"unauthorized", fmt.Errorf("check: %w", model.ErrUnauthorized), "/approve isn't allowed here."},
		{"unavailable", fmt.Errorf("open store: %w", model.ErrUnavailable), "/approve didn't work: your project data isn't loaded. Restart Marshal from your project folder."},
		{"no runtime", fmt.Errorf("attach: %w", errNoRuntime), "/approve didn't work: your project data isn't loaded. Restart Marshal from your project folder."},
		{"deadline", fmt.Errorf("call: %w", context.DeadlineExceeded), "/approve took too long and was stopped. Try again."},
		{"invalid", fmt.Errorf("%w: session ID is required", model.ErrInvalid), "/approve didn't work: session ID is required."},
		{"invalid already punctuated", fmt.Errorf("%w: give a run id?", model.ErrInvalid), "/approve didn't work: give a run id?"},
		{"other", errors.New("boom: open /home/x/state.db: no such file"), "/approve didn't work. The details are in .marshal/logs/tui-errors.log."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := plainCommandError("/approve apr", tc.err)
			if got != tc.want {
				t.Fatalf("plainCommandError = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "%!") {
				t.Fatalf("message %q contains a formatting error", got)
			}
			if strings.Contains(got, "Error:") {
				t.Fatalf("message %q leaks the Go error prefix", got)
			}
			if strings.Contains(got, ": /") {
				t.Fatalf("message %q leaks a filesystem path", got)
			}
		})
	}
}

// The empty command line falls back to a subject, never an empty word.
func TestPlainCommandErrorEmptyLine(t *testing.T) {
	got := plainCommandError("   ", model.ErrNotFound)
	if want := "That didn't work: nothing by that name was found."; got != want {
		t.Fatalf("plainCommandError(empty) = %q, want %q", got, want)
	}
}

// An unmapped error is shown as is only when it is already one plain sentence;
// any chain, path, internal ID or overlong text gets the generic answer.
func TestPlainCommandErrorFallbackRule(t *testing.T) {
	const generic = "/doctor didn't work. The details are in .marshal/logs/tui-errors.log."
	for _, tc := range []struct{ text, want string }{
		{"Install Codex to use this command.", "Install Codex to use this command."},
		{"/codex is not installed here.", "/codex is not installed here."},
		{strings.Repeat("a", 160), strings.Repeat("a", 160)},
		{"read runs: open state failed", generic},
		{"two\nlines", generic},
		{"cannot open /home/me/project/state.db", generic},
		{"look in /var/state", generic},
		{"see main.go", generic},
		{"see marshal.log", generic},
		{"look in ~/x", generic},
		{"look in /tmp", generic},
		{"Error while running", generic},
		{"run RUN-abc123 failed", generic},
		{"plan PLAN-1 is old", generic},
		{"INSTANCE-9 lost", generic},
		{"gate CONST_ALLOWED", generic},
		{strings.Repeat("a", 161), generic},
	} {
		if got := plainCommandError("/doctor codex", errors.New(tc.text)); got != tc.want {
			t.Errorf("plainCommandError(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

// An error converted to a plain sentence at its source shows that sentence and
// still logs its technical cause.
func TestPlainSentenceErrorShowsTextAndLogsCause(t *testing.T) {
	err := plainSentence("There is no approved plan to run yet.", errors.New("execution plan not found"))
	if got, want := plainCommandError("/marshal use-plan", err), "There is no approved plan to run yet."; got != want {
		t.Fatalf("plainCommandError = %q, want %q", got, want)
	}
	tmp := t.TempDir()
	ws := NewWorkspace(nil, "project", "session")
	ws.workDir = tmp
	ws.logTUIError("/marshal use-plan", err)
	data, readErr := os.ReadFile(statepath.Path(tmp, "logs", "tui-errors.log"))
	if readErr != nil || !strings.Contains(string(data), "/marshal use-plan: There is no approved plan to run yet. (cause: execution plan not found)") {
		t.Fatalf("log = %q, %v", data, readErr)
	}
}

// A failing command keeps its full technical detail in the project's log file
// while the screen stays plain.
func TestCommandErrorIsLogged(t *testing.T) {
	tmp := t.TempDir()
	ws := NewWorkspace(nil, "project", "session")
	ws.workDir = tmp

	ws.recordCommandResult("/approve apr", "", fmt.Errorf("read runs: %w", model.ErrNotFound))

	data, err := os.ReadFile(statepath.Path(tmp, "logs", "tui-errors.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("log has %d lines, want 1: %q", len(lines), string(data))
	}
	if !strings.Contains(lines[0], "not found") {
		t.Fatalf("log line %q does not carry the error", lines[0])
	}
	if strings.Contains(lines[0], "\n") {
		t.Fatalf("log line %q contains a raw newline", lines[0])
	}
}

// The error log never stores a secret from the command line or the error.
func TestCommandErrorLogIsRedacted(t *testing.T) {
	tmp := t.TempDir()
	ws := NewWorkspace(nil, "project", "session")
	ws.workDir = tmp
	ws.logTUIError("/login password=hunter2", fmt.Errorf("refused: api_key=abc"))
	data, err := os.ReadFile(statepath.Path(tmp, "logs", "tui-errors.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for _, leaked := range []string{"hunter2", "api_key=abc"} {
		if strings.Contains(string(data), leaked) {
			t.Fatalf("log %q contains %q", string(data), leaked)
		}
	}
}

// A Marshal launch that did not happen reads as a plain sentence; the cause is
// still reachable through Unwrap for the log.
func TestMarshalStartErrorIsPlain(t *testing.T) {
	cause := errors.New("hidden instruction delivery failed: x")
	err := marshalNotStarted("codex", cause)
	want := "The Marshal couldn't start with Codex because its instructions could not be handed over. Press m to try again, or check it on the Providers view (press 4)."
	if err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
	if errors.Unwrap(err) != cause {
		t.Fatalf("Unwrap() = %v, want %v", errors.Unwrap(err), cause)
	}
}

// Every failed Marshal launch says why in one plain line, and that line is
// what a command shows; the raw cause never reaches the line (finding #82).
func TestMarshalStartErrorSaysWhy(t *testing.T) {
	cases := []struct {
		cause error
		why   string
	}{
		{fmt.Errorf("MCP delivery failed: %w", redactedCause{projectcontext.ErrBudget}), "because the project's planning context is too large to hand over."},
		{fmt.Errorf("MCP delivery failed: %w", redactedCause{projectcontext.Changed("x.go:1")}), "because the project changed while its planning context was being read."},
		{fmt.Errorf("MCP delivery failed: %w", redactedCause{errors.New("proposal MCP project binding differs")}), "because its Marshal tools could not be connected."},
		{errors.New("read-only launch settings failed: denied"), "because its read-only settings could not be applied."},
		{fmt.Errorf("tmux window not opened: %w", redactedCause{errors.New("no server")}), "because its tmux window could not be opened."},
		{errors.New("something else entirely"), "because its program failed to launch."},
	}
	for _, c := range cases {
		err := marshalNotStarted("codex", c.cause)
		line := plainCommandError("/marshal chat", err)
		if !strings.HasPrefix(line, "The Marshal couldn't start with Codex "+c.why) {
			t.Fatalf("cause %q: line = %q, want reason %q", c.cause, line, c.why)
		}
		if strings.Contains(line, c.cause.Error()) || strings.Contains(line, "REQUIRED_CONTEXT") {
			t.Fatalf("line %q leaks the raw cause", line)
		}
		if !errors.Is(err, errors.Unwrap(c.cause)) && errors.Unwrap(c.cause) != nil {
			t.Fatalf("cause %q is no longer reachable through errors.Is", c.cause)
		}
	}
}

// A failed /marshal chat puts its raw cause in Activity and in
// tui-errors.log, while the answer on the Board stays the plain line.
func TestMarshalStartFailureCauseReachesActivityAndLog(t *testing.T) {
	tmp := t.TempDir()
	ws := NewWorkspace(nil, "project", "session")
	ws.workDir = tmp
	cause := fmt.Errorf("MCP delivery failed: %w", redactedCause{projectcontext.ErrBudget})
	ws.recordCommandResult("/marshal chat", "", marshalNotStarted("codex", cause))

	ws.mu.Lock()
	answer := ws.answer
	ws.mu.Unlock()
	if !strings.HasPrefix(answer, "The Marshal couldn't start with Codex because the project's planning context is too large") || strings.Contains(answer, "REQUIRED_CONTEXT") {
		t.Fatalf("Board answer = %q", answer)
	}
	found := false
	for _, n := range ws.noteList() {
		if strings.Contains(n.Text, "Why the Marshal didn't start: MCP delivery failed: REQUIRED_CONTEXT_BUDGET_EXCEEDED") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Activity lacks the raw cause: %+v", ws.noteList())
	}
	data, err := os.ReadFile(statepath.Path(tmp, "logs", "tui-errors.log"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "(cause: MCP delivery failed: REQUIRED_CONTEXT_BUDGET_EXCEEDED)") {
		t.Fatalf("log = %q", data)
	}
}

// A Marshal that fails to start on its own (startup, restart) logs the cause
// and records it in Activity too.
func TestMarshalStartFailureReportedOutsideCommands(t *testing.T) {
	tmp := t.TempDir()
	ws := NewWorkspace(nil, "project", "session")
	ws.workDir = tmp
	if ws.reportMarshalStartFailure(errors.New("not a launch failure")) {
		t.Fatal("an ordinary error was reported as a Marshal start failure")
	}
	if !ws.reportMarshalStartFailure(marshalNotStarted("codex", errors.New("hidden instruction delivery failed: token=abc"))) {
		t.Fatal("launch failure not reported")
	}
	data, err := os.ReadFile(statepath.Path(tmp, "logs", "tui-errors.log"))
	if err != nil || !strings.Contains(string(data), "marshal start: The Marshal couldn't start with Codex because its instructions could not be handed over.") || !strings.Contains(string(data), "(cause: hidden instruction delivery failed") {
		t.Fatalf("log = %q, %v", data, err)
	}
	found := false
	for _, n := range ws.noteList() {
		found = found || strings.HasPrefix(n.Text, "Why the Marshal didn't start: hidden instruction delivery failed")
	}
	if !found {
		t.Fatalf("Activity lacks the cause: %+v", ws.noteList())
	}
}
