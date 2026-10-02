package tui

import (
	"strings"
	"testing"
)

func TestDarwinSandboxStatus(t *testing.T) {
	text := sandboxStatusForOS("darwin")
	if !strings.Contains(text, "BLOCKED") || !strings.Contains(text, "no macOS sandbox backend") {
		t.Fatalf("status: %s", text)
	}
}
