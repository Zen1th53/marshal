package lanes

import (
	"encoding/base64"
	"testing"
)

// Fixture test: OSC 7501 sequences from Claude Code, Codex, omp -> states
func TestOSC7501Fixtures(t *testing.T) {
	claudeMsg := base64.StdEncoding.EncodeToString([]byte("Running tests in internal/tui/..."))
	codexMsg := base64.StdEncoding.EncodeToString([]byte("Editing internal/tui/board/widgets/lanes/lanes.go"))
	ompMsg := base64.StdEncoding.EncodeToString([]byte("Analyzing workspace AST"))

	tests := []struct {
		name         string
		sequence     string
		wantState    string
		wantKind     string
		wantApp      string
		wantMsg      string
		wantProgress int
	}{
		{
			name:         "Claude Code working state with ST escape",
			sequence:     "\x1b]7501;state=working:app=claude:msg=" + claudeMsg + "\x1b\\",
			wantState:    "working",
			wantApp:      "claude",
			wantMsg:      "Running tests in internal/tui/...",
			wantProgress: -1,
		},
		{
			name:         "Codex blocked on permission with BEL terminator",
			sequence:     "\x1b]7501;state=blocked:kind=permission:app=codex:msg=" + codexMsg + "\x07",
			wantState:    "blocked",
			wantKind:     "permission",
			wantApp:      "codex",
			wantMsg:      "Editing internal/tui/board/widgets/lanes/lanes.go",
			wantProgress: -1,
		},
		{
			name:         "omp working with progress",
			sequence:     "\x1b]7501;state=working:app=omp:progress=45:msg=" + ompMsg + "\x1b\\",
			wantState:    "working",
			wantApp:      "omp",
			wantMsg:      "Analyzing workspace AST",
			wantProgress: 45,
		},
		{
			name:         "Claude Code done",
			sequence:     "\x1b]7501;state=done:app=claude\x1b\\",
			wantState:    "done",
			wantApp:      "claude",
			wantProgress: -1,
		},
		{
			name:         "Codex error",
			sequence:     "\x1b]7501;state=error:app=codex:msg=" + base64.StdEncoding.EncodeToString([]byte("Compilation failure")) + "\x1b\\",
			wantState:    "error",
			wantApp:      "codex",
			wantMsg:      "Compilation failure",
			wantProgress: -1,
		},
		{
			name:         "Multiple sequences in output picks latest",
			sequence:     "some preceding text\x1b]7501;state=working:app=omp\x1b\\more text\x1b]7501;state=blocked:kind=question:app=omp\x07",
			wantState:    "blocked",
			wantKind:     "question",
			wantApp:      "omp",
			wantProgress: -1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseOSC7501(tc.sequence)
			if !ok {
				t.Fatalf("ParseOSC7501(%q) failed to find sequence", tc.sequence)
			}
			if got.State != tc.wantState {
				t.Errorf("state = %q, want %q", got.State, tc.wantState)
			}
			if got.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if got.App != tc.wantApp {
				t.Errorf("app = %q, want %q", got.App, tc.wantApp)
			}
			if got.Msg != tc.wantMsg {
				t.Errorf("msg = %q, want %q", got.Msg, tc.wantMsg)
			}
			if got.Progress != tc.wantProgress {
				t.Errorf("progress = %d, want %d", got.Progress, tc.wantProgress)
			}
		})
	}
}
