package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyDiagnosticsNeverCreateDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if st, err := OpenReadOnly(t.Context(), path); err == nil {
		st.Close()
		t.Fatal("opened missing state")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("database was created: %v", err)
	}
}
