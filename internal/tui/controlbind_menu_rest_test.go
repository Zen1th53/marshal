package tui

import (
	"strings"
	"testing"
)

// A waiver expiry that is not a time is refused in words, not with Go's
// reference layout (e2e 2026-11-06, Authorized finding closure).
func TestWaiverExpiryRefusalIsReadable(t *testing.T) {
	err := validateWaiverExpiry("zz/q?bad id")
	if err == nil || strings.Contains(err.Error(), "2006-01-02T15:04:05Z07:00") || !strings.Contains(err.Error(), "RFC 3339") {
		t.Fatalf("got %v", err)
	}
	if err := validateWaiverExpiry("2026-11-06T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
}
