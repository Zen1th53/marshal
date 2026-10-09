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
