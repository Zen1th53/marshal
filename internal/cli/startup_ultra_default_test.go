//go:build governed

package cli

import (
	"encoding/json"
	"testing"

	"github.com/Zen1th53/marshal/internal/startup"
)

func TestCommunitySetupDoesNotGrantUltraAtStartup(t *testing.T) {
	output, stderr, code := runCLI(t, plainDirectory(t), "--json", "setup", "status")
	if code != 0 {
		t.Fatalf("setup status exited %d: %s", code, stderr)
	}
	var assessment startup.Assessment
	if err := json.Unmarshal([]byte(output), &assessment); err != nil {
		t.Fatal(err)
	}
	if assessment.Has(startup.CapUltra) {
		t.Fatal("Community setup granted ULTRA without verified cloud entitlement")
	}
}
