package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Zen1th53/marshal/internal/model"
)

// DefaultVerificationCommand preserves the pack runner where it is installed,
// and gives ordinary projects an actionable error before invoking a process.
func DefaultVerificationCommand(root string) ([]string, error) {
	info, err := os.Stat(filepath.Join(root, "conformance", "runner.py"))
	if os.IsNotExist(err) || (err == nil && !info.Mode().IsRegular()) {
		return nil, fmt.Errorf("%w: no verification runner configured: provide conformance/runner.py or specify a verification command with marshal verify -- COMMAND [ARGS...] (commands must be allowed by the verification policy)", model.ErrInvalid)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect verification runner: %w", err)
	}
	return []string{"python", "conformance/runner.py", "validate-pack"}, nil
}
