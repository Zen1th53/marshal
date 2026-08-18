package protocol

import (
	"testing"
	"time"
)

func BenchmarkA09ValidateTypedHandoff(b *testing.B) {
	handoff := Handoff{
		ID:             "HANDOFF-BENCHMARK",
		Version:        Version1,
		TaskID:         "TASK-BENCHMARK",
		FromAgent:      "AGENT-developer",
		ToRole:         RoleQA,
		Status:         StatusCreated,
		Claims:         map[string]string{"summary": "ready"},
		EvidenceIDs:    []EvidenceID{"EVIDENCE-BENCHMARK"},
		ChangedFiles:   []string{"internal/protocol/engine.go"},
		ContextDigest:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAt:      time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC),
		IdempotencyKey: "benchmark-request",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := handoff.Validate(); err != nil {
			b.Fatal(err)
		}
	}
}
