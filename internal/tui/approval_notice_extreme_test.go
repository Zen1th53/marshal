//go:build !governed

package tui

import (
	"strings"
	"testing"
)

func TestExtremeTaskApprovalNoticeNamesTheMarshal(t *testing.T) {
	got := taskApprovalNotice("fix-add", "app-1")
	if strings.Contains(got, "waiting for approval") || !strings.Contains(got, "Marshal is deciding approval app-1") {
		t.Fatalf("notice reads as an owner wait: %q", got)
	}
}
