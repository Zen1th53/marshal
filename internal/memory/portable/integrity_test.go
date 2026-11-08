package portable_test

import (
	"context"
	"github.com/Zen1th53/marshal/internal/memory/portable"
	"github.com/Zen1th53/marshal/internal/model"
	"testing"
)

func TestPortableImportRejectsPayloadTamperEvenInDryRun(t *testing.T) {
	m := portable.NewManager()
	pack, err := m.Export(context.Background(), []model.MemoryRecordV2{{ID: "memory", Body: "reviewed", Lifecycle: model.MemoryCandidate}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pack.Records[0].Body = "substituted"
	for _, dry := range []bool{false, true} {
		if _, err = m.Import(context.Background(), pack, portable.ImportOptions{DryRun: dry}); err == nil {
			t.Fatalf("tampered payload accepted dry=%t", dry)
		}
	}
}
