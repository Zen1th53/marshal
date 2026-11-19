//go:build !governed

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestProjectBaselineCoversExistingCode(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"main.go", "internal/core/core.go", "internal/core/other.go", "internal/ui/ui.go", "web/app.ts", ".marshal/private.go", "node_modules/library/index.js"} {
		file := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("source"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := projectcontext.Contract{Components: []projectcontext.Component{{ID: "goal", Paths: []string{"internal/core/core.go"}}}}
	err := validateProjectBaseline(t.Context(), root, c)
	for _, part := range []string{"main.go", "internal/core", "internal/ui", "web"} {
		if err == nil || !strings.Contains(err.Error(), part) {
			t.Fatalf("missing %s from baseline refusal: %v", part, err)
		}
	}
	c.Components = []projectcontext.Component{{ID: "app", Paths: []string{"main.go", "internal", "web"}}}
	if err := validateProjectBaseline(t.Context(), root, c); err != nil {
		t.Fatal(err)
	}
	c.Components[0].Removed = true
	if err := validateProjectBaseline(t.Context(), root, c); err == nil {
		t.Fatal("removed parts cannot cover the baseline")
	}
}
