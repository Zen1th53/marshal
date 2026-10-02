package doctor

import (
	"context"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/resources"
	"strings"
	"testing"
)

func TestDarwinDoctorReportsSandboxCapability(t *testing.T) {
	for _, available := range []bool{false, true} {
		result := seatbeltResult(model.IsolationCapability{Level: model.IsolationSeatbelt, Available: available, Reason: "seatbelt probe result; no process namespace"})
		want := Degraded
		if available {
			want = Pass
		}
		if result.Name != "seatbelt" || result.Verdict != want || !strings.Contains(result.Detail, "no process namespace") {
			t.Fatalf("result: %#v", result)
		}
	}
	var results []Result
	probeBwrapForOS(context.Background(), "darwin", func(string) (string, error) { t.Fatal("unexpected PATH lookup"); return "", nil }, nil, func(r Result) { results = append(results, r) })
	if len(results) != 1 || results[0].Name != "seatbelt" {
		t.Fatalf("results: %#v", results)
	}
}

func TestDarwinDoctorDoesNotFabricateAvailableMemory(t *testing.T) {
	text := resourceDetailForOS(resources.Snapshot{}, "darwin")
	if !strings.Contains(text, "memory inventory unavailable on this platform") || strings.Contains(text, "RAM available") {
		t.Fatalf("resources: %s", text)
	}
}
