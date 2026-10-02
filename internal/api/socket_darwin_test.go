//go:build darwin

package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestSocketPathTooLongFailsBeforeFilesystemChanges(t *testing.T) {
	err := prepareSocket("/" + strings.Repeat("x", 103))
	if !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "fewer than 104 bytes") {
		t.Fatalf("socket length error: %v", err)
	}
}
