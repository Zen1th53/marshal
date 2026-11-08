package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Ordinary tests cannot discover a host model CLI by its well-known command
// name or project.FindBinary's home/system fallback. Explicit live-model tests
// remain a separate opt-in. A test needing a CLI installs its owned double.
func isolateOrdinaryTestProviderPath() (func(), error) {
	if os.Getenv("MARSHAL_REAL_MODEL") == "1" {
		return func() {}, nil
	}
	if os.Getenv("MARSHAL_TEST_MEMORY_CHILD_ROOT") != "" || os.Getenv("MARSHAL_TEST_MEMORY_CRASH_ROOT") != "" {
		return func() {}, nil
	}
	original := os.Getenv("PATH")
	dir, err := os.MkdirTemp("", "marshal-tui-test-tools-")
	if err != nil {
		return nil, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	complete := false
	defer func() {
		if !complete {
			cleanup()
		}
	}()
	excluded := map[string]bool{"codex": true, "claude": true, "opencode": true, "agy": true, "antigravity": true, "gemini": true}
	var ordered []string
	seen := map[string]string{}
	for index, entry := range filepath.SplitList(original) {
		if entry == "" {
			entry = "."
		}
		absolute, err := filepath.Abs(entry)
		if err != nil {
			return nil, err
		}
		realDir, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			realDir = absolute
		}
		if prevMapped, ok := seen[realDir]; ok {
			ordered = append(ordered, prevMapped)
			continue
		}
		files, err := os.ReadDir(absolute)
		if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read original test tool directory: %w", err)
		}
		mapped := filepath.Join(dir, fmt.Sprintf("%04d", index))
		if err := os.Mkdir(mapped, 0700); err != nil {
			return nil, err
		}
		ordered = append(ordered, mapped)
		seen[realDir] = mapped
		for _, file := range files {
			name := file.Name()
			if excluded[name] {
				continue
			}
			target := filepath.Join(absolute, name)
			info, err := os.Stat(target)
			if err != nil {
				continue
			}
			if info.IsDir() || info.Mode().Perm()&0111 == 0 {
				continue
			}
			// Resolve the original directory before any test changes CWD. Keeping
			// every original PATH entry preserves first-choice precedence and later
			// same-name executables used by wrappers such as ccache.
			if err = os.Symlink(target, filepath.Join(mapped, name)); err != nil {
				return nil, err
			}
		}
	}
	if err = os.Setenv("PATH", strings.Join(ordered, string(os.PathListSeparator))); err != nil {
		return nil, err
	}
	if err = os.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1"); err != nil {
		return nil, err
	}
	complete = true
	return cleanup, nil
}
