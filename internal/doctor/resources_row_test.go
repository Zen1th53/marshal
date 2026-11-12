package doctor

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/resources"
)

func TestResourcesRowNamesVendorOnceAndWarnsOnCriticalPressure(t *testing.T) {
	snapshot := resources.Snapshot{
		CPU:            resources.CPU{Effective: 16},
		Memory:         resources.Memory{AvailableBytes: 15 << 30},
		Storage:        resources.Storage{FreeBytes: 13 << 30},
		Recommendation: resources.Recommendation{Concurrency: 1},
		Accelerators: []resources.Accelerator{
			{Vendor: "Intel", Model: "Intel Graphics", TelemetrySource: "UNKNOWN"},
			{Vendor: "NVIDIA", Model: "GeForce RTX 4070", TelemetrySource: "nvidia-smi"},
		},
		Health: resources.Health{Overall: resources.StatusCritical, Disk: resources.StatusCritical, Warnings: []string{"very low disk space"}},
	}
	row := resourcesResult(snapshot)
	if strings.Contains(row.Detail, "Intel Intel") {
		t.Fatalf("vendor named twice: %q", row.Detail)
	}
	if !strings.Contains(row.Detail, "accelerators: Intel Graphics (telemetry: UNKNOWN), NVIDIA GeForce RTX 4070 (telemetry: nvidia-smi)") {
		t.Fatalf("accelerators not shown plainly: %q", row.Detail)
	}
	if row.Verdict != Warn || !strings.Contains(row.Detail, "(critical pressure: very low disk space)") {
		t.Fatalf("critical pressure row = %s %q, want WARN naming the reason", row.Verdict, row.Detail)
	}

	// Pressure is advisory: a WARN row never changes the report's verdict.
	report := Report{Verdict: Pass}
	add := reportAdder(&report)
	add(row)
	if report.Verdict != Pass {
		t.Fatalf("a pressure warning changed the verdict to %s", report.Verdict)
	}

	snapshot.Health = resources.Health{Overall: resources.StatusWarn, Warnings: []string{"low available RAM"}}
	if row := resourcesResult(snapshot); row.Verdict != Warn || !strings.Contains(row.Detail, "(pressure warning: low available RAM)") {
		t.Fatalf("warning pressure row = %s %q", row.Verdict, row.Detail)
	}
	snapshot.Health = resources.Health{Overall: resources.StatusOK}
	if row := resourcesResult(snapshot); row.Verdict != Pass || strings.Contains(row.Detail, "pressure") {
		t.Fatalf("healthy row = %s %q", row.Verdict, row.Detail)
	}
}
