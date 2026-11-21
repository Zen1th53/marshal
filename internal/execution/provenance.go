package execution

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateAllowedBinary verifies that a resolved binary does not execute from untrusted/shadowed directories.
func ValidateAllowedBinary(resolvedPath string, blockedDirs []string) error {
	clean := filepath.Clean(resolvedPath)

	// Block current working directory shadowing or temp directory execution unless explicitly permitted
	if blockedDirs == nil {
		blockedDirs = []string{"/tmp", "/var/tmp", "./", "."}
	}

	for _, b := range blockedDirs {
		if (b == "." || b == "./") && (!filepath.IsAbs(clean) || strings.HasPrefix(resolvedPath, ".")) {
			return fmt.Errorf("%w: binary %s located in untrusted/shadowed directory %s",
				ErrUnauthorizedWorker, clean, b)
		}
		cleanB := filepath.Clean(b)
		if filepath.IsAbs(cleanB) {
			if strings.HasPrefix(clean, cleanB+"/") || clean == cleanB {
				return fmt.Errorf("%w: binary %s located in untrusted/shadowed directory %s",
					ErrUnauthorizedWorker, clean, cleanB)
			}
		}
	}

	return nil
}
