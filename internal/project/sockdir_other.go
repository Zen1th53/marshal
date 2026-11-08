//go:build !unix

package project

import (
	"fmt"
	"os"
)

func ensureShortSocketDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create socket directory %s: %w", dir, err)
	}
	return nil
}
