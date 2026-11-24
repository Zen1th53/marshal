package tui

import (
	"os"
	"strings"
	"testing"
)

func TestApprovalNoticeInboxDeduplication(t *testing.T) {
	root := t.TempDir()
	for range 2 {
		view, err := openMarshalInbox(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := view.writeNotice("approval:ticket:terminal", "Approval expired\n"); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(inboxPath(root, marshalInboxReader))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "Approval expired"); got != 1 {
		t.Fatalf("rendered notices: %d", got)
	}
}
