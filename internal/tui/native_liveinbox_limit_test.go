package tui

import (
	"errors"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/model"
)

// An inbox view of exactly its bound is left as it is; one byte more drops
// the oldest entries, note included, to stay inside the bound and keeps the
// newest with a durable cursor. A single entry larger than the view keeps its
// newest bytes.
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
	if cur := view.Cursor(); cur == "" || !strings.Contains(over, "cursor: "+cur) {
		t.Fatalf("drop note missing cursor %q:\n%s", cur, over[:min(len(over), 800)])
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

// The live inbox lists unanswered items ahead of regular entries, preserves
// unanswered items when trimming past the byte cap, and pages durable entries
// (archived and live) with a reader-scoped cursor.
func TestInboxViewCursorAndUnansweredFirst(t *testing.T) {
	root := t.TempDir()
	view, err := openMarshalInbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := view.write("## regular-1\n\n" + strings.Repeat("a", 24<<10) + "\n\n"); err != nil {
		t.Fatal(err)
	}
	if err := view.writeNotice("approval:A1:opened", "## MARSHAL approval opened · A1\n\nWaiting for decision.\n\n"); err != nil {
		t.Fatal(err)
	}
	if err := view.writeUnanswered("q-1", "## Question q-1\n\nNeeds answer.\n\n"); err != nil {
		t.Fatal(err)
	}
	// Regular entries push the view over 64 KiB; unanswered items stay at the
	// front while older regular entries move into the durable archive.
	if err := view.write("## regular-2\n\n" + strings.Repeat("b", 24<<10) + "\n\n"); err != nil {
		t.Fatal(err)
	}
	if err := view.write("## regular-3\n\n" + strings.Repeat("c", 24<<10) + "\n\n"); err != nil {
		t.Fatal(err)
	}
	if err := view.trim(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(view.path)
	if err != nil {
		t.Fatal(err)
	}
	live := string(raw)
	if len(live) > viewMaxBytes {
		t.Fatalf("live inbox exceeded cap: %d bytes", len(live))
	}
	posA1 := strings.Index(live, "## MARSHAL approval opened · A1")
	posQ1 := strings.Index(live, "## Question q-1")
	posReg3 := strings.Index(live, "## regular-3")
	if posA1 < 0 || posQ1 < 0 || posReg3 < 0 || !(posA1 < posQ1 && posQ1 < posReg3) {
		t.Fatalf("unanswered items not listed first: A1=%d Q1=%d reg3=%d", posA1, posQ1, posReg3)
	}
	if strings.Contains(live, "## regular-1") {
		t.Fatal("oldest regular entry was not trimmed to make room")
	}
	dropCursor := view.Cursor()
	if dropCursor == "" || !strings.Contains(live, "cursor: "+dropCursor) {
		t.Fatalf("missing drop cursor %q", dropCursor)
	}

	// Paging through the durable inbox returns unanswered items first, then
	// archived and live regular items across pages without loss.
	var collected []inboxPageEntry
	cursor := ""
	pages := 0
	for {
		page, err := view.Page(cursor, 28<<10)
		if err != nil {
			t.Fatalf("page %d (%q): %v", pages, cursor, err)
		}
		pages++
		collected = append(collected, page.Entries...)
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" || page.Limit == nil || page.Limit.Kind != "live_inbox" {
			t.Fatalf("page %d missing cursor/limit: %+v", pages, page)
		}
		cursor = page.NextCursor
	}
	if pages < 2 || len(collected) < 5 {
		t.Fatalf("expected multi-page walk over all entries, got %d pages %d entries", pages, len(collected))
	}
	if !collected[0].Unanswered || !strings.Contains(collected[0].Text, "approval opened · A1") {
		t.Fatalf("first paged item not unanswered A1: %+v", collected[0])
	}
	if !collected[1].Unanswered || !strings.Contains(collected[1].Text, "Question q-1") {
		t.Fatalf("second paged item not unanswered q-1: %+v", collected[1])
	}
	foundArchived := false
	for _, item := range collected[2:] {
		if item.Unanswered {
			t.Fatalf("unanswered item appeared after regular items: %+v", item)
		}
		if strings.Contains(item.Text, "## regular-1") {
			foundArchived = true
		}
	}
	if !foundArchived {
		t.Fatal("archived entry regular-1 missing from paged inbox walk")
	}

	// A cursor from another reader is refused.
	if _, err := view.Page("inbox:codex:1", 4096); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("foreign cursor err = %v, want ErrPolicyDenied", err)
	}

	// Answering items removes them from the unanswered-first section.
	if err := view.writeNotice("approval:A1:terminal", "## MARSHAL approval approved · A1\n\nApproved.\n\n"); err != nil {
		t.Fatal(err)
	}
	if err := view.markAnswered("q-1"); err != nil {
		t.Fatal(err)
	}
	after, err := view.Page("", viewMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range after.Entries {
		if item.Unanswered {
			t.Fatalf("answered item remained marked unanswered: %+v", item)
		}
	}
}
