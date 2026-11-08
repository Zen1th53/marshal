//go:build !governed

package marshal

import (
	"encoding/json"
	"testing"
)

func TestExtremeConcurrencySettingsRoundTripAndValidation(t *testing.T) {
	original := DefaultSettings()
	original.GlobalCap = 9
	original.ProviderLimits = ProviderLimits{Codex: 3, Claude: 1, Agy: 2, OpenCode: 3}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Settings
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GlobalCap != 9 || decoded.ProviderLimits != original.ProviderLimits {
		t.Fatalf("concurrency settings lost: %s", raw)
	}
	for _, field := range []string{"global", "codex", "claude", "agy", "opencode"} {
		s := DefaultSettings()
		switch field {
		case "global":
			s.GlobalCap = -1
		case "codex":
			s.ProviderLimits.Codex = -1
		case "claude":
			s.ProviderLimits.Claude = -1
		case "agy":
			s.ProviderLimits.Agy = -1
		case "opencode":
			s.ProviderLimits.OpenCode = -1
		}
		if err = s.Validate(); err == nil {
			t.Fatalf("negative %s limit accepted", field)
		}
	}
}
