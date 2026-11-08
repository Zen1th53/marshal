//go:build !governed

package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// agy reports an exhausted model quota inside its JSON envelope; the failed
// turn must name it so the lifecycle fails the task over until the reset.
func TestAgyQuotaFailureFailsOverUntilReset(t *testing.T) {
	quota := "Individual quota reached. Please upgrade your subscription to increase your limits. Resets in 100h11m27s."
	stdout := []byte(`{"status":"ERROR","is_error":true,"response":"` + quota + `"}`)
	result := adapter.Result{Status: adapter.StatusFailure, ExitCode: 1, FinalText: quota, Stdout: stdout}
	msg := antigravityFailureMessage(false, result, stdout, nil)
	if !strings.HasPrefix(msg, "Antigravity provider limit: ") || !strings.Contains(msg, "Resets in 100h11m27s") {
		t.Fatalf("quota cause lost: %q", msg)
	}
	limited, reason, cooldown := classifyProviderLimitAt(errors.New(msg), marshal.HandIn{}, nil, time.Now())
	want := 100*time.Hour + 11*time.Minute + 27*time.Second
	if !limited || reason == "" || cooldown != want {
		t.Fatalf("limit=%v reason=%q cooldown=%v, want %v", limited, reason, cooldown, want)
	}
	plain := adapter.Result{Status: adapter.StatusFailure, ExitCode: 1, Stdout: []byte(`{"status":"ERROR","response":"tool failed"}`)}
	if msg := antigravityFailureMessage(false, plain, plain.Stdout, nil); msg != "Antigravity execution did not complete successfully" {
		t.Fatalf("ordinary failure renamed: %q", msg)
	}
	if limited, _, _ := classifyProviderLimitAt(errors.New(antigravityFailureMessage(false, plain, plain.Stdout, nil)), marshal.HandIn{}, nil, time.Now()); limited {
		t.Fatal("ordinary failure classified as a provider limit")
	}
	for _, text := range []string{"Resets in 3m20s", "resets in 2h5m"} {
		if d, ok := parseCooldownText(text, time.Now()); !ok || d <= 0 {
			t.Fatalf("compound reset %q not parsed: %v %v", text, d, ok)
		}
	}
	if d, ok := parseCooldownText("Resets in 45m", time.Now()); !ok || d != 45*time.Minute {
		t.Fatalf("single-unit reset changed: %v %v", d, ok)
	}
}
