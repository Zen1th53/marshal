package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestRemoteErrorDoesNotRepeatTheSentinelPrefix(t *testing.T) {
	err := remoteError(&Error{Code: "invalid_input", Message: "invalid input: agent name is required"})
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("lost the sentinel: %v", err)
	}
	if got := err.Error(); strings.Count(got, "invalid input") != 1 {
		t.Fatalf("doubled prefix: %q", got)
	}
}
