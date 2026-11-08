package integration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// This fixture has no provider work. Preserve each original tool directory
// and duplicate executable chain while excluding every provider basename.
func isolateIntegrationTUIProviderPath(t *testing.T) {
	t.Helper()
	excluded := map[string]bool{"codex": true, "claude": true, "opencode": true, "agy": true, "antigravity": true, "gemini": true}
	root := t.TempDir()
	var ordered []string
	for index, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == "" {
			entry = "."
		}
		absolute, err := filepath.Abs(entry)
		if err != nil {
			t.Fatal(err)
		}
		files, err := os.ReadDir(absolute)
		if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		mapped := filepath.Join(root, fmt.Sprintf("%04d", index))
		if err := os.Mkdir(mapped, 0700); err != nil {
			t.Fatal(err)
		}
		ordered = append(ordered, mapped)
		for _, file := range files {
			name := file.Name()
			if excluded[name] {
				continue
			}
			target := filepath.Join(absolute, name)
			info, err := os.Stat(target)
			if err != nil || info.IsDir() || info.Mode().Perm()&0111 == 0 {
				continue
			}
			if err := os.Symlink(target, filepath.Join(mapped, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("PATH", strings.Join(ordered, string(os.PathListSeparator)))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
}
