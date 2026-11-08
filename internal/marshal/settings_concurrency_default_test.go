//go:build governed

package marshal

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultSettingsExcludeExtremeConcurrency(t *testing.T) {
	raw, err := json.Marshal(DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"ProviderLimits", "GlobalCap", "codex", "claude", "agy", "opencode"} {
		if strings.Contains(string(raw), `"`+field+`"`) {
			t.Fatalf("default settings exposed extreme concurrency: %s", raw)
		}
	}
}
