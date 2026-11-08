package app

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// A provider that reports its usage limit only on stdout and exits 1 with an
// empty stderr still names the limit in the turn's error, so the limited
// reviewer is recognised and skipped.
func TestMarshalTurnErrorKeepsStdoutWhenStderrIsEmpty(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 1").Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatal("no exit error")
	}
	stdout := []byte(`{"type":"error","message":"You've hit your usage limit. Try again at Nov 4th, 2026 2:22 PM."}`)
	got := marshalTurnError(err, stdout)
	if !errors.Is(got, err) || !strings.Contains(got.Error(), "usage limit") || providerLimitReason([]byte(got.Error())) == "" {
		t.Fatalf("limit message lost: %v", got)
	}
	if plain := marshalTurnError(err, nil); plain.Error() != "Marshal model turn: "+err.Error() {
		t.Fatalf("empty output changed the error: %v", plain)
	}
	long := marshalTurnError(err, []byte(strings.Repeat("x", 5000)))
	if !strings.HasSuffix(long.Error(), " [truncated]") || len(long.Error()) > 4200 {
		t.Fatalf("long output not bounded: %d", len(long.Error()))
	}
}
