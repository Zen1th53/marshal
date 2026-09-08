package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The learning command is registered and fails closed on a bare invocation.
func TestLearningCommandIsRegisteredAndFailClosed(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), ".", []string{"learning"}, strings.NewReader(""), &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "marshal learning commit") {
		t.Fatalf("code=%d output=%q", code, errOut.String())
	}
}

// An unknown subcommand is refused rather than silently ignored.
func TestLearningRejectsUnknownSubcommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), ".", []string{"learning", "promote-everything"}, strings.NewReader(""), &out, &errOut)
	if code == 0 {
		t.Fatalf("unknown subcommand succeeded: %q", out.String())
	}
}

// A search without a project is refused: retrieval is always bounded.
func TestLearningSearchRequiresProject(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), ".", []string{"learning", "search"}, strings.NewReader(""), &out, &errOut)
	if code == 0 {
		t.Fatalf("unbounded search succeeded: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "--project") {
		t.Fatalf("error did not name the missing flag: %q", errOut.String())
	}
}

// An unmeasured metric renders as "unmeasured", never as zero.
func TestOptionalIntKeepsUnmeasuredDistinctFromZero(t *testing.T) {
	if got := optionalInt(nil); got != "unmeasured" {
		t.Fatalf("optionalInt(nil) = %q, want \"unmeasured\"", got)
	}
	zero := int64(0)
	if got := optionalInt(&zero); got != "0" {
		t.Fatalf("optionalInt(0) = %q, want \"0\"", got)
	}
}
