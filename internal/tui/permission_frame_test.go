package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/permission"
)

func TestPermissionFrameKeepsHeadingWhenBodyClipsIntoRequest(t *testing.T) {
	for _, tc := range []struct {
		name     string
		requests []permission.Request
	}{
		{"network batch", []permission.Request{
			{Kind: "network", Object: "first.example.test:443", RunID: "RUN-network", TaskID: "T1", Who: "worker-with-a-long-name"},
			{Kind: "network", Object: "second.example.test:443", RunID: "RUN-network", TaskID: "T2", Who: "codex"},
		}},
		{"single read", []permission.Request{
			{Kind: "read", Object: "/tmp/earlier-work", Scope: "this session only, read-only", Who: "Marshal", Reason: "Continue earlier work"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const cols, rows = 80, 24
			text, err := permission.Render(tc.requests)
			if err != nil {
				t.Fatal(err)
			}
			th := NewTheme(ThemeNoColor, false, false)
			w := NewWorkspace(nil, "project", "session")
			w.RecordActivity(text)
			frame := BuildFrame(w.GetUIState(), th, "", NewComposer(th), nil, cols, rows)
			firstItem := -1
			for i, line := range frame.Body {
				if strings.HasPrefix(strings.TrimSpace(line), "1. ") {
					firstItem = i
					break
				}
			}
			if firstItem < 0 {
				t.Fatal("permission items absent from frame body")
			}
			// Later sections make the default tail viewport start at the first
			// item, reproducing the heading loss in the control centre.
			bodyHeight := rows - len(frame.Header) - 2 - len(frame.Composer)
			for len(frame.Body) < firstItem+bodyHeight {
				frame.Body = append(frame.Body, "")
			}
			lines, _ := frame.Lines(cols, rows)
			if len(lines) != rows {
				t.Fatalf("frame has %d rows, want %d", len(lines), rows)
			}
			got := strings.Join(lines, "\n")
			// Compare every nonempty popup line, including headings, items and
			// key help, with the existing control-centre width truncation.
			for _, line := range strings.Split(text, "\n") {
				if line != "" && !strings.Contains(got, PadCell("   "+line, cols)) {
					t.Errorf("missing rendered permission line %q:\n%s", line, got)
				}
			}
		})
	}
}

func TestPermissionFrameDoesNotPullOlderHeadingOverLatestPrompt(t *testing.T) {
	const cols = 80
	prompt := func(object string) []string {
		text, err := permission.Render([]permission.Request{{Kind: "network", Object: object}})
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			lines = append(lines, PadCell("   "+line, cols))
		}
		return lines
	}
	for _, tc := range []struct {
		name       string
		prefix     int
		gap        int
		tail       int
		bodyHeight int
		fake       bool
	}{
		{name: "older footer still visible", prefix: 2, gap: 6, bodyHeight: 18},
		{name: "older footer above viewport", prefix: 5, gap: 6, tail: 8, bodyHeight: 20},
		{name: "ordinary activity heading", prefix: 5, gap: 6, tail: 8, bodyHeight: 20, fake: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := make([]string, tc.prefix)
			if tc.fake {
				body = append(body, PadCell("   Permission request", cols))
			} else {
				body = append(body, prompt("old.example.test:443")...)
			}
			body = append(body, make([]string, tc.gap)...)
			latest := len(body)
			body = append(body, prompt("pending.example.test:443")...)
			body = append(body, make([]string, tc.tail)...)
			start := len(body) - tc.bodyHeight
			if start <= tc.prefix || latest < start {
				t.Fatal("fixture must clip the old heading and fully show the latest prompt")
			}
			lines, _ := (Frame{Body: body}).Lines(cols, tc.bodyHeight+3)
			for i, want := range body[start:] {
				if lines[i] != want {
					t.Fatalf("body row %d = %q, want %q; viewport moved away from latest prompt", i, lines[i], want)
				}
			}
		})
	}
}

func TestPermissionFrameKeepsLatestPromptWithActivityAndTeam(t *testing.T) {
	const cols, rows = 80, 24
	th := NewTheme(ThemeNoColor, false, false)
	w := NewWorkspace(nil, "project", "session")
	for _, object := range []string{"old.example.test:443", "pending.example.test:443"} {
		text, err := permission.Render([]permission.Request{{Kind: "network", Object: object}})
		if err != nil {
			t.Fatal(err)
		}
		w.RecordActivity(text)
		if strings.HasPrefix(object, "old.") {
			w.RecordActivity("Permission request resolved: old.example.test:443 — denied")
			w.RecordActivity("Worker continues after the decision")
		}
	}
	frame := BuildFrame(w.GetUIState(), th, "", NewComposer(th), nil, cols, rows)
	lines, _ := frame.Lines(cols, rows)
	got := strings.Join(lines, "\n")
	text, _ := permission.Render([]permission.Request{{Kind: "network", Object: "pending.example.test:443"}})
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if !strings.Contains(got, PadCell("   "+line, cols)) {
			t.Errorf("latest prompt line missing: %q", line)
		}
	}
	if !strings.Contains(got, "Team") {
		t.Error("fully visible latest prompt must not displace the trailing team section")
	}
}
