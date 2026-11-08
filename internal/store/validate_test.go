package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateInitializedNeverCreatesMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	if err := ValidateInitialized(t.Context(), path); err == nil {
		t.Fatal("accepted missing canonical state")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("validation created canonical state: %v", err)
	}
}
