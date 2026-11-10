package usage

import (
	"os"
	"path/filepath"
	"testing"
)

func claudeFrom(t *testing.T, name string) Quota {
	t.Helper()
	return readClaude(now, filepath.Join("testdata", "claude", name))
}

func TestClaudeFixtures(t *testing.T) {
	for _, tc := range []struct {
		file    string
		known   bool
		stale   bool   // only when known
		reason  string // only when unknown
		binding string // binding window label when known
		used    int    // UsedShown of the binding window when known
	}{
		{file: "fresh.json", known: true, stale: false, binding: "wk", used: 42},
		{file: "old-3h.json", known: true, stale: true, binding: "wk", used: 42},
		{file: "old-25h.json", known: false, reason: ReasonStale},
		{file: "reset-partial.json", known: true, stale: false, binding: "wk", used: 42},
		{file: "reset-all.json", known: false, reason: ReasonReset},
		{file: "no-cache.json", known: false, reason: ReasonNotReported},
		{file: "null-windows.json", known: false, reason: ReasonNotReported},
		{file: "out-of-range.json", known: true, stale: false, binding: "wk", used: 42},
		{file: "out-of-range-all.json", known: false, reason: ReasonOutOfRange},
		{file: "string-util.json", known: false, reason: ReasonMalformed},
		{file: "missing-fetched.json", known: false, reason: ReasonMissing},
		{file: "trailing.json", known: false, reason: ReasonMalformed},
	} {
		t.Run(tc.file, func(t *testing.T) {
			q := claudeFrom(t, tc.file)
			if q.Provider != "claude" {
				t.Fatalf("provider = %q", q.Provider)
			}
			if q.Known != tc.known {
				t.Fatalf("Known = %v, want %v (%+v)", q.Known, tc.known, q)
			}
			if !tc.known {
				if q.Reason != tc.reason {
					t.Fatalf("Reason = %q, want %q", q.Reason, tc.reason)
				}
				if !q.AsOf.IsZero() || q.Stale {
					t.Fatalf("unknown quota must carry no AsOf/Stale: %+v", q)
				}
				return
			}
			if q.Stale != tc.stale {
				t.Fatalf("Stale = %v, want %v", q.Stale, tc.stale)
			}
			if q.AsOf.IsZero() {
				t.Fatalf("known quota must carry AsOf: %+v", q)
			}
			w, ok := q.Binding()
			if !ok || w.Label != tc.binding {
				t.Fatalf("Binding = %+v (ok %v), want label %q", w, ok, tc.binding)
			}
			if w.UsedShown() != tc.used {
				t.Fatalf("UsedShown = %d, want %d", w.UsedShown(), tc.used)
			}
		})
	}
}

func TestClaudeFreshWindows(t *testing.T) {
	q := claudeFrom(t, "fresh.json")
	if len(q.Windows) != 2 {
		t.Fatalf("windows = %+v", q.Windows)
	}
	if q.Windows[0].Label != "5h" || q.Windows[0].UsedShown() != 12 || q.Windows[0].LeftPercent != 88 {
		t.Fatalf("5h = %+v", q.Windows[0])
	}
	if q.Windows[1].Label != "wk" || q.Windows[1].UsedShown() != 42 || q.Windows[1].LeftPercent != 58 {
		t.Fatalf("wk = %+v", q.Windows[1])
	}
}

func TestClaudePartialWindows(t *testing.T) {
	// reset-partial: the 5h window has reset, so it is unknown; the weekly
	// one still binds.
	q := claudeFrom(t, "reset-partial.json")
	if len(q.Windows) != 2 || q.Windows[0].Known() || q.Windows[0].Label != "5h" {
		t.Fatalf("reset 5h window must be unknown: %+v", q.Windows)
	}
	// out-of-range: the 5h window is out of range, the weekly one binds.
	q = claudeFrom(t, "out-of-range.json")
	if !q.Known || q.Windows[0].Known() {
		t.Fatalf("out-of-range 5h window must be unknown: %+v", q)
	}
}

func TestClaudeNoFile(t *testing.T) {
	if q := readClaude(now, ""); q.Known || q.Reason != ReasonNoFile {
		t.Fatalf("empty path = %+v", q)
	}
	if q := readClaude(now, filepath.Join(t.TempDir(), "nope.json")); q.Known || q.Reason != ReasonNoFile {
		t.Fatalf("missing file = %+v", q)
	}
}

func TestClaudeConfigFile(t *testing.T) {
	// Absolute override: used directly.
	t.Setenv("CLAUDE_CONFIG_DIR", "/opt/claude")
	if got := ClaudeConfigFile(); got != filepath.Join("/opt/claude", ".claude.json") {
		t.Fatalf("absolute = %q", got)
	}
	// Relative override: unusable, never the home file.
	t.Setenv("CLAUDE_CONFIG_DIR", "relative/dir")
	if got := ClaudeConfigFile(); got != "" {
		t.Fatalf("relative = %q, want empty", got)
	}
	// Unset: the home file.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, err := os.UserHomeDir()
	if err == nil {
		if got := ClaudeConfigFile(); got != filepath.Join(home, ".claude.json") {
			t.Fatalf("unset = %q, want %q", got, filepath.Join(home, ".claude.json"))
		}
	}
}
