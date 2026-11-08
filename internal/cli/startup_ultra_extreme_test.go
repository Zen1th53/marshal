//go:build !governed

package cli

import (
	"encoding/json"
	"testing"

	"github.com/Zen1th53/marshal/internal/startup"
)

func TestExtremeSetupReportsExistingLocalUltraGrant(t *testing.T) {
	dir := plainDirectory(t)
	before := listDir(t, dir)
	output, stderr, code := runCLI(t, dir, "--json", "setup", "status")
	if code != 0 {
		t.Fatalf("setup status exited %d: %s", code, stderr)
	}
	var assessment startup.Assessment
	if err := json.Unmarshal([]byte(output), &assessment); err != nil {
		t.Fatal(err)
	}
	if !assessment.Has(startup.CapUltra) {
		t.Fatalf("private local ULTRA grant missing from readiness: %s", output)
	}
	found := false
	for _, check := range assessment.Checks {
		if check.ID == "env.ultra" {
			found = true
			if check.Status != startup.StatusReady || check.Reason != startup.ReasonOK {
				t.Fatalf("existing private local grant reported unavailable: %+v", check)
			}
		}
	}
	if !found {
		t.Fatal("ULTRA readiness check omitted")
	}
	if after := listDir(t, dir); len(after) != len(before) {
		t.Fatalf("assessment created project/cloud state: before=%v after=%v", before, after)
	}
}
