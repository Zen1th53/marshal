package app

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
)

//go:embed defaults/*.yaml
var projectDefaults embed.FS

var defaultProjectFiles = []string{
	"CAPABILITIES.yaml",
	"PACK-VERSION.yaml",
	"RUNTIME-VERSION.yaml",
}

func ensureProjectDefaults(root string) error {
	for _, name := range defaultProjectFiles {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		switch {
		case err == nil:
			if !info.Mode().IsRegular() {
				return fmt.Errorf("project default %s is not a regular file", path)
			}
			continue
		case !os.IsNotExist(err):
			return fmt.Errorf("inspect project default %s: %w", path, err)
		}

		data, err := fs.ReadFile(projectDefaults, pathpkg.Join("defaults", name))
		if err != nil {
			return fmt.Errorf("read embedded project default %s: %w", name, err)
		}
		if err := createProjectDefault(path, data); err != nil {
			return err
		}
	}
	return nil
}

func createProjectDefault(path string, data []byte) (retErr error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create project default %s: %w", path, err)
	}
	remove := true
	defer func() {
		if closeErr := file.Close(); retErr == nil && closeErr != nil {
			retErr = fmt.Errorf("close project default %s: %w", path, closeErr)
		}
		if remove || retErr != nil {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write project default %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync project default %s: %w", path, err)
	}
	remove = false
	return nil
}
