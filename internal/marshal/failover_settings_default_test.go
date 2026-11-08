//go:build governed

package marshal

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultSettingsExcludeFailover(t *testing.T) {
	b, err := json.Marshal(DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "failover") {
		t.Fatalf("private settings leaked: %s", b)
	}
}
