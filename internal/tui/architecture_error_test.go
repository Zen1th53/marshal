package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// The footer names the real reason the architecture could not be opened,
// not a generic "try again in a moment" that waiting will never fix.
func TestPlainArchitectureErrorCarriesCause(t *testing.T) {
	err := fmt.Errorf("the project summary cannot be drawn: %w", errors.New("architecture component IDs must be exact and unique"))
	if got := plainArchitectureError(err); got != "the project summary cannot be drawn: architecture component IDs must be exact and unique." {
		t.Fatalf("cause: %q", got)
	}
	long := errors.New(strings.Repeat("x", 400) + "\nsecond line")
	if got := plainArchitectureError(long); len([]rune(got)) != maxArchitectureCause || !strings.HasSuffix(got, "…") || strings.Contains(got, "second") {
		t.Fatalf("long cause: %q", got)
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := plainArchitectureError(err); got != "try again in a moment" {
			t.Fatalf("%v: %q", err, got)
		}
	}
	if got := plainArchitectureError(os.ErrNotExist); got != "the Marshal has not drafted it yet" {
		t.Fatalf("not drafted: %q", got)
	}
}
