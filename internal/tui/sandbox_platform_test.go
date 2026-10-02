package tui

import (
	"github.com/Zen1th53/marshal/internal/model"
	"strings"
	"testing"
)

func TestDarwinSandboxStatus(t *testing.T) {
	for _, available := range []bool{false, true} {
		text := seatbeltStatus(model.IsolationCapability{Level: model.IsolationSeatbelt, Available: available, Reason: "seatbelt: no process namespace"})
		want := "BLOCKED"
		if available {
			want = "AVAILABLE"
		}
		if !strings.Contains(text, want) || !strings.Contains(text, "no process namespace") {
			t.Fatalf("status: %s", text)
		}
	}
}
