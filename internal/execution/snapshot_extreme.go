//go:build !governed

package execution

import (
	"fmt"
	"os"
	"path/filepath"
)

// A committed private run revision includes both the file contents and the
// containing directory entry, not merely an atomic but unflushed rename.
func writeRunSnapshot(path string, data []byte) error {
	// Preserve refusal of a conflicting non-regular legacy staging path. An
	// older writer or unexpected filesystem object is not an owned temp file.
	if info, err := os.Lstat(path + ".tmp"); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: conflicting run staging path", ErrRunConflict)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".run-snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
