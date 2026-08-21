package version

import (
	"strings"
	"testing"
)

func TestVersionReporting(t *testing.T) {
	info := Current()
	if info.Version == "" {
		t.Fatal("expected non-empty version")
	}
	if info.Commit == "" {
		t.Fatal("expected non-empty commit")
	}
	if info.BuildDate == "" {
		t.Fatal("expected non-empty build date")
	}
	rendered := info.String()
	if !strings.Contains(rendered, "marshal") {
		t.Fatalf("expected marshal prefix in string output: %s", rendered)
	}
}
