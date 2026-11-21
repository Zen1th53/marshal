//go:build !governed

package tui

import (
	"strings"
	"testing"
)

// The nudge after a browser Accept names the file the acceptance note is in,
// not "your inbox", which a Marshal read as its provider-named channel view.
func TestBrowserAcceptNudgeNamesTheRuntimeInbox(t *testing.T) {
	if !strings.Contains(browserAcceptNudge, marshalInboxFile) {
		t.Fatalf("nudge does not name %s: %q", marshalInboxFile, browserAcceptNudge)
	}
}
