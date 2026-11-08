//go:build !governed

package tui

import (
	"github.com/Zen1th53/marshal/internal/marshal"
	"testing"
)

func TestExtremeFailoverSettings(t *testing.T) {
	var s marshal.Settings
	if _, handled, err := handleFailoverSetting([]string{"failover-order"}, &s); !handled || err == nil {
		t.Fatal("missing argument accepted")
	}
	if _, _, err := handleFailoverSetting([]string{"failover-order", "claude", "->", "codex"}, &s); err != nil {
		t.Fatal(err)
	}
	if s.FailoverOrder != "claude,codex" {
		t.Fatalf("order %s", s.FailoverOrder)
	}
	if _, err := parseFailoverOrder("codex,codex"); err == nil {
		t.Fatal("duplicate accepted")
	}
}
