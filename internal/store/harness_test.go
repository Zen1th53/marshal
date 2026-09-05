package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestHarnessProfileStoreAndRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "marshal_harness_test.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	profile := model.HarnessProfile{
		Harness:          "antigravity",
		InstalledVersion: "2.1.0",
		BinaryPath:       "/usr/local/bin/agy",
		SupportedModels:  []string{"gemini-2.5-pro", "gemini-2.5-flash"},
		DefaultModel:     "gemini-2.5-pro",
		FeatureSupport: map[string]model.FeatureStatus{
			"instructions":      model.StatusNative,
			"headless":          model.StatusNative,
			"structured_output": model.StatusNative,
			"mcp_client":        model.StatusNative,
			"sandbox":           model.StatusNative,
			"artifacts":         model.StatusNative,
		},
		ReasoningKnobs:  []string{"thought_intensity", "high", "medium"},
		NativeModes:     []string{"headless_worker", "ide_bridge"},
		ProbeEvidenceID: "ev-agy-probe-01",
		ProbedAt:        now,
		ExpiresAt:       now.Add(12 * time.Hour),
	}

	// 1. Save harness profile
	if err := st.SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatalf("SaveHarnessProfile failed: %v", err)
	}

	// 2. Restart store
	if err := st.Close(); err != nil {
		t.Fatalf("close store failed: %v", err)
	}

	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen store failed: %v", err)
	}
	defer st2.Close()

	// 3. Verify profile survived restart
	retrieved, err := st2.GetHarnessProfile(ctx, "antigravity")
	if err != nil {
		t.Fatalf("GetHarnessProfile failed: %v", err)
	}

	if retrieved.InstalledVersion != "2.1.0" {
		t.Errorf("expected version 2.1.0, got %s", retrieved.InstalledVersion)
	}
	if retrieved.FeatureSupport["artifacts"] != model.StatusNative {
		t.Errorf("expected artifacts status native, got %v", retrieved.FeatureSupport["artifacts"])
	}
	if !retrieved.IsFresh(now.Add(1 * time.Hour)) {
		t.Errorf("expected profile to be fresh 1h later")
	}
	if retrieved.IsFresh(now.Add(24 * time.Hour)) {
		t.Errorf("expected profile to be expired 24h later")
	}
}
