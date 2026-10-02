package doctor

import (
	"context"
	"github.com/Zen1th53/marshal/internal/resources"
	"strings"
	"testing"
)

func TestDarwinDoctorReportsBlockedSandbox(t *testing.T) {
	var results []Result
	probeBwrapForOS(context.Background(), "darwin", func(string) (string, error) { t.Fatal("unexpected binary lookup"); return "", nil }, nil, func(r Result) { results = append(results, r) })
	if len(results) != 1 || results[0].Verdict != Degraded || !strings.Contains(results[0].Detail, "no macOS sandbox backend") || results[0].Capability != "governed execution blocked" {
		t.Fatalf("results: %#v", results)
	}
}

func TestDarwinDoctorDoesNotFabricateAvailableMemory(t *testing.T) {
	text := resourceDetailForOS(resources.Snapshot{}, "darwin")
	if !strings.Contains(text, "memory inventory unavailable on this platform") || strings.Contains(text, "RAM available") {
		t.Fatalf("resources: %s", text)
	}
}
