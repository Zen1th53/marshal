package app

import (
	"errors"
	"strings"
	"testing"
)

func TestProviderDisabledStopNamesTheSetting(t *testing.T) {
	cause := errors.New("empty failover order")
	var err error = &ProviderDisabledError{Provider: "codex", Cause: cause}
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "provider codex is disabled") {
		t.Fatalf("error = %v", err)
	}
	var known interface{ StopReason() (string, string) }
	if !errors.As(err, &known) {
		t.Fatal("disabled provider stop has no stop reason")
	}
	reason, next := known.StopReason()
	if !strings.Contains(reason, "codex") || !strings.Contains(reason, "turned off") || !strings.Contains(next, "Settings") {
		t.Fatalf("reason=%q next=%q", reason, next)
	}
}
