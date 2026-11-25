package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

func TestProviderWaitText(t *testing.T) {
	retry := `{"type":"error","message":"Reconnecting... 2/5 (Selected model is at capacity. Please try a different model.)"}`
	cases := []struct{ name, out, want string }{
		{"capacity", "{\"type\":\"turn.started\"}\n" + retry + "\n", "codex is at capacity, retrying (2/5)"},
		{"cleared by progress", retry + "\n{\"type\":\"item.completed\"}\n", ""},
		{"reconnecting", `{"type":"error","message":"Reconnecting... 1/5 (stream closed)"}`, "codex is reconnecting (1/5)"},
		{"rate limit text", "API Error: 429 rate limit exceeded\n", "codex is rate limited, retrying"},
		{"quiet", "building\n", ""},
	}
	for _, c := range cases {
		if got := providerWaitText("codex", c.out); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestProviderWaitReadsRelayAndHomeShowsIt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "task-sign-relay-latest.txt")
	if err := os.WriteFile(p, []byte(`{"type":"error","message":"Reconnecting... 2/5 (Selected model is at capacity.)"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := vm.Board{Tasks: []vm.Task{{ID: "sign", State: "building", Provider: providerWait("codex", p)}}}
	if got := strings.Join(homeTasks(b), ""); got != "sign building · codex is at capacity, retrying (2/5)" {
		t.Fatalf("tasks line %q", got)
	}
}

// A number in the worker's own output is not a rate limit, and an old wait
// the worker has printed past is over.
func TestProviderWaitIgnoresOldAndIncidentalNumbers(t *testing.T) {
	for _, out := range []string{
		"ok  \texample.com/calc\t0.429s\n",
		"API Error: 429 rate limit\nRetrying worked, now editing sign.go\n",
		"want 529 items\n",
	} {
		if got := providerWaitText("claude", out); got != "" {
			t.Errorf("%q gave %q, want nothing", out, got)
		}
	}
}
