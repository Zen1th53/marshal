//go:build governed

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommunityLegacyMemoryMigrationRefusesBeforeOpeningAnyState(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"migrate-legacy", "rollback-migration"} {
		var out, stderr bytes.Buffer
		if code := Execute(t.Context(), root, []string{"memory", command}, strings.NewReader(""), &out, &stderr); code == 0 || !strings.Contains(stderr.String(), "Marshal build") {
			t.Fatal("Community exposed private migration", code, stderr.String())
		}
		if _, err := os.Stat(filepath.Join(root, ".marshal")); !os.IsNotExist(err) {
			t.Fatal("Community migration opened state")
		}
		if _, err := os.Stat(filepath.Join(root, ".marshal-extreme")); !os.IsNotExist(err) {
			t.Fatal("Community migration initialized private state")
		}
	}
}
