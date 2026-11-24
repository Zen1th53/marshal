package tui

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// An inbox view of exactly its bound is left as it is; one byte more drops
// the oldest entries, note included, to stay inside the bound and keeps the
// newest. A single entry larger than the view keeps its newest bytes.
func TestInboxViewLimitAtCap(t *testing.T) {
	root := t.TempDir()
	view, err := openInboxView(root, "codex", true)
	if err != nil {
		t.Fatal(err)
	}
	read := func() string {
		data, err := os.ReadFile(view.path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	first := "## old entry\n\n" + strings.Repeat("o", 20<<10) + "\n\n"
	if err := view.write(first); err != nil {
		t.Fatal(err)
	}
	newest := "## newest entry\n\n"
	pad := viewMaxBytes - len(read()) - len(newest) - len("\n")
	if err := view.write(newest + strings.Repeat("n", pad) + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := view.trim(); err != nil {
		t.Fatal(err)
	}
	at := read()
	if len(at) != viewMaxBytes || !strings.Contains(at, "## old entry") || strings.Contains(at, "dropped") {
		t.Fatalf("at bound: %d bytes", len(at))
	}
	if err := view.write("e"); err != nil {
		t.Fatal(err)
	}
	if err := view.trim(); err != nil {
		t.Fatal(err)
	}
	over := read()
	if len(over) > viewMaxBytes || strings.Contains(over, "## old entry") || !strings.Contains(over, "older entr") || !strings.HasSuffix(over, "n\ne") || !strings.Contains(over, "## newest entry") {
		t.Fatalf("one over: %d bytes", len(over))
	}

	huge := "## huge entry\n\n" + strings.Repeat("東", viewMaxBytes) + "tail"
	if err := view.write(huge); err != nil {
		t.Fatal(err)
	}
	if err := view.trim(); err != nil {
		t.Fatal(err)
	}
	cut := read()
	if len(cut) > viewMaxBytes || !utf8.ValidString(cut) || !strings.HasSuffix(cut, "東tail") || !strings.Contains(cut, "start of the newest entry cut") || !strings.HasPrefix(cut, viewHeader("codex")) {
		t.Fatalf("single huge entry: %d bytes", len(cut))
	}
}
