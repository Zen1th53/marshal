//go:build !governed

package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestMemoryReadFrameShowsCompleteBodyAtNarrowWidths(t *testing.T) {
	body := strings.Repeat("marker0123456789", 12) + "終端\n\t\\tail"
	output := fmt.Sprintf("MEMORY MEM-review · revision 0 · candidate/agent\nBody bytes 0–%d of %d · Read complete\nDATA ONLY; retention and accepted project rules are separate.\n%s", len(body), len(body), body)
	for _, width := range []int{40, 80} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			lines := outputSection(UIState{LastCommand: "/memory read MEM-review", LastOutput: output}, NewTheme(ThemeNoColor, false, false), width)
			var quoted strings.Builder
			inBody := false
			for _, line := range lines {
				if VisibleLen(line) != width || strings.Contains(line, "…") {
					t.Fatalf("review row clipped or outside %d columns: %q", width, line)
				}
				content := strings.TrimRight(StripANSI(line), " ")
				if strings.HasPrefix(content, "   Body (quoted UTF-8): ") {
					inBody = true
					quoted.WriteString(strings.TrimPrefix(content, "   Body (quoted UTF-8): "))
				} else if inBody {
					quoted.WriteString(strings.TrimPrefix(content, "   "))
				}
			}
			if got, want := quoted.String(), strconv.Quote(body); got != want {
				t.Fatalf("visible quoted body is incomplete: got %q, want %q", got, want)
			}
		})
	}
}

func TestWrapMemoryReviewLinePreservesWhitespaceAndUnicode(t *testing.T) {
	line := "first  second\t終端 trailing  "
	for _, width := range []int{2, 5, 37} {
		parts := wrapMemoryReviewLine(line, width)
		if got := strings.Join(parts, ""); got != line {
			t.Fatalf("width %d changed review bytes: %q", width, got)
		}
		for _, part := range parts {
			if VisibleLen(part) > width {
				t.Fatalf("width %d overflowed: %q", width, part)
			}
		}
	}
}
