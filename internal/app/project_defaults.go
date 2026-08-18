package app

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed defaults/*.yaml
var projectDefaults embed.FS

var defaultProjectFiles = []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"}

func ensureProjectDefaults(root string) error {
	for _, name := range defaultProjectFiles {
		path := filepath.Join(root, name)
		if _, err := os.Lstat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect project contract %s: %w", name, err)
		}
		data, err := fs.ReadFile(projectDefaults, filepath.Join("defaults", name))
		if err != nil {
			return fmt.Errorf("read embedded project contract %s: %w", name, err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create project contract %s: %w", name, err)
		}
		if _, err := file.Write(data); err != nil {
			file.Close()
			return fmt.Errorf("write project contract %s: %w", name, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close project contract %s: %w", name, err)
		}
	}
	return nil
}
