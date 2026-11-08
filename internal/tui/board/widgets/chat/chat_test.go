package chat

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rivo/uniseg"
)

var updateGolden = flag.Bool("update-chat-golden", false, "rewrite testdata/*.golden")

func at(hm string) time.Time {
	t, _ := time.Parse("15:04", hm)
	return t
}

// mockup02 is the conversation of mockup 02 (Marshal split), in English.
func mockup02() *View {
	v := &View{}
	v.SetHeader(Header{Provider: "claude", Role: "planner/reviewer", State: "plan v2 approved 13:41"})
	v.Append(Entry{Role: You, At: at("14:01"), Text: "Why is T-023 parked?"})
	v.Final(at("14:01"), "msg_1", "T-023 (codex-3) wanted to run `npm view deja-vu`. The manifest names only api.openai.com and github.com; registry.npmjs.org is not in the plan.\nThe argument carries taint U (package.json came from outside).\nSo: park. The lane finished 2 files in the meantime; the decision is in the inbox.")
	v.ToolUse(at("14:01"), "tu_1", "Bash", "npm view deja-vu --json")
	v.ToolResult("tu_1", "denied in the inbox", true)
	v.Final(at("14:01"), "msg_2", "Proposal: [A] allow for this run - low risk (read-only registry, 0 files outside).")
	v.Append(Entry{Role: You, At: at("14:02"), Text: "A. And explain T-031"})
	v.ToolUse(at("14:02"), "tu_2", "Read", "internal/netpolicy/broker_npm.go")
	v.ToolResult("tu_2", "212 lines", false)
	v.Delta(at("14:02"), "msg_3", "T-023 allow recorded, decided_by: mandate:M-7f3a-23 - the lane continues.\n")
	v.Delta(at("14:02"), "msg_3", "T-031 hard gate: `git push --force` - irreversible. I will not decide it; type the canonical id. Blast radius: origin/main, 3 tags.")
	v.SetThinking(true)
	return v
}

func render(v *View, w, h int) string {
	lines, row, col := v.Render(w, h, Style{})
	var b strings.Builder
	for _, l := range lines {
		if n := uniseg.StringWidth(l); n > w {
			panic(fmt.Sprintf("line wider than %d: %d %q", w, n, l))
		}
		b.WriteString("|" + l + strings.Repeat(" ", w-uniseg.StringWidth(l)) + "|\n")
	}
	fmt.Fprintf(&b, "cursor %d,%d\n", row, col)
	return b.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update-chat-golden to create it)", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch:\nGOT:\n%s\nWANT:\n%s", name, got, want)
	}
}

func TestChatGoldens(t *testing.T) {
	for _, w := range []int{56, 80, 120} {
		v := mockup02()
		v.SetFocused(true)
		v.Insert("type canonical id")
		golden(t, fmt.Sprintf("chat-%03dx30.golden", w), render(v, w, 30))
	}
}

func TestChatGoldenUnicodeAndScrolled(t *testing.T) {
	v := mockup02()
	v.Scroll(5)
	lines, _, _ := v.Render(80, 16, Style{Unicode: true})
	golden(t, "chat-080x16-unicode-scrolled.golden", strings.Join(lines, "\n")+"\n")
}

func TestChatRendersEveryRowWithinWidth(t *testing.T) {
	v := mockup02()
	for w := 1; w <= 130; w += 7 {
		for h := 1; h <= 40; h += 3 {
			lines, row, col := v.Render(w, h, Style{Unicode: true})
			if len(lines) != h {
				t.Fatalf("%dx%d: %d lines", w, h, len(lines))
			}
			for _, l := range lines {
				if uniseg.StringWidth(l) > w {
					t.Fatalf("%dx%d: line too wide %q", w, h, l)
				}
			}
			if row != h || col < 1 {
				t.Fatalf("%dx%d: cursor %d,%d", w, h, row, col)
			}
		}
	}
}

func TestChatStreamingSettlesWithoutDuplication(t *testing.T) {
	v := &View{}
	v.Delta(at("10:00"), "m1", "Hel")
	v.Delta(at("10:00"), "m1", "lo")
	v.Final(at("10:00"), "m1", "Hello.")
	v.ToolUse(at("10:00"), "t1", "Bash", "ls")
	v.Delta(at("10:00"), "m1", "Done")
	v.Final(at("10:00"), "m1", "Done.")
	var texts []string
	for _, e := range v.Entries() {
		if e.Role == Marshal {
			texts = append(texts, e.Text)
			if e.Streaming {
				t.Fatalf("still streaming: %+v", e)
			}
		}
	}
	if strings.Join(texts, "|") != "Hello.|Done." {
		t.Fatalf("segments %q", texts)
	}
	if got := v.LastReply(); got != "Hello.\n\nDone." {
		t.Fatalf("LastReply %q", got)
	}
}

func TestChatStripsEscapesAndControls(t *testing.T) {
	v := &View{}
	v.Final(time.Time{}, "m", "red \x1b[31mtext\x1b[0m \x1b]52;c;ZXZpbA==\x07bell\x07 ‮flip")
	lines, _, _ := v.Render(60, 6, Style{})
	joined := strings.Join(lines, "\n")
	if strings.ContainsAny(joined, "\x1b\x07‮") {
		t.Fatalf("escape reached the frame: %q", joined)
	}
	if !strings.Contains(joined, "red text bell flip") {
		t.Fatalf("text lost: %q", joined)
	}
}

func TestChatComposerEditing(t *testing.T) {
	v := &View{}
	v.Insert("helo")
	v.Move(-1)
	v.Insert("l")
	v.End()
	v.Insert(" world\n")
	v.Home()
	v.Delete()
	v.Insert("H")
	if v.Text() != "Hello world " {
		t.Fatalf("text %q", v.Text())
	}
	v.Backspace()
	if got := v.Take(); got != "ello world" && got != "Hello world" {
		t.Fatalf("take %q", got)
	}
	if v.Text() != "" {
		t.Fatal("take left text")
	}
	// A long line keeps the cursor on screen.
	v.Insert(strings.Repeat("x", 200))
	_, _, col := v.Render(40, 5, Style{})
	if col > 40 {
		t.Fatalf("cursor off screen: %d", col)
	}
}

func TestChatVersionOnlyMovesOnChange(t *testing.T) {
	v := mockup02()
	before := v.Version()
	for range 5 {
		v.Render(80, 30, Style{Unicode: true})
	}
	v.SetThinking(true)
	v.SetFocused(false)
	v.ScrollEnd()
	if v.Version() != before {
		t.Fatalf("rendering or no-op setters changed the version: %d -> %d", before, v.Version())
	}
	v.Delta(time.Time{}, "msg_3", "!")
	if v.Version() == before {
		t.Fatal("a delta did not change the version")
	}
}

func TestChatHasNoEmojiInChrome(t *testing.T) {
	for _, st := range []Style{{}, {Unicode: true}} {
		v := mockup02()
		v.Scroll(3)
		lines, _, _ := v.Render(120, 30, st)
		for _, l := range lines {
			for _, r := range l {
				if r >= 0x1F000 || (r >= 0x2600 && r <= 0x27BF && r != '✓' && r != '✗') {
					t.Fatalf("emoji-range rune %U in %q", r, l)
				}
			}
		}
	}
}
