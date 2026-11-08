package app

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/verification"
)

// Another installed provider is preferred for a role; with only the
// Marshal's own provider installed, that one takes the role in a new session
// instead of the role being refused.
func TestRoleProviderPrefersAnotherButNeverRefuses(t *testing.T) {
	cases := []struct {
		installed []string
		avoid     []string
		want      string
	}{
		{[]string{"codex", "claude", "agy"}, []string{"claude", "codex"}, "agy"},
		{[]string{"codex", "claude"}, []string{"claude", "codex"}, "claude"},
		{[]string{"claude"}, []string{"claude", "claude"}, "claude"},
		{nil, []string{"codex"}, "codex"},
	}
	for _, c := range cases {
		if got := roleProvider(c.installed, c.avoid...); got != c.want {
			t.Errorf("roleProvider(%v, %v) = %q, want %q", c.installed, c.avoid, got, c.want)
		}
	}
}

// A role's model CLI never resumes a conversation: that is the rule that
// keeps a reviewer or verifier from seeing another role's work in progress.
func TestRoleStartsInAFreshSession(t *testing.T) {
	cli := freshRoleCLI("claude", "/repo")
	if cli.ConversationID != "" || cli.Provider != "claude" || cli.Dir != "/repo" {
		t.Fatalf("role CLI %+v would not start a fresh session", cli)
	}
}

// With a single installed provider the Marshal's own provider verifies ULTRA
// work in a fresh session; only a missing verifier is refused.
func TestSingleProviderCanVerifyUltra(t *testing.T) {
	s := &MarshalService{ModelProvider: "codex", IndependentVerify: func(context.Context, marshal.Run, string, verification.Session) (marshal.VerifierEvidence, error) {
		return marshal.VerifierEvidence{}, nil
	}}
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "codex", nil }
	if err := s.requireIndependentVerifier(context.Background(), marshal.Run{Tier: marshal.Ultra}); err != nil {
		t.Fatalf("a single provider could not verify: %v", err)
	}
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "", nil }
	if err := s.requireIndependentVerifier(context.Background(), marshal.Run{Tier: marshal.Ultra}); err == nil {
		t.Fatal("a missing verifier was accepted")
	}
}

// A provider other than the worker's is chosen whenever one is installed.
func TestPickIndependentProviderPrefersDifferent(t *testing.T) {
	got, same := pickIndependentProvider([]string{"codex", "claude", "agy"}, []string{"codex"}, "codex")
	if got != "claude" || same {
		t.Fatalf("got %q same=%v, want claude false", got, same)
	}
	// The Marshal's own provider is acceptable when it is the only
	// non-worker left.
	got, same = pickIndependentProvider([]string{"codex", "claude"}, []string{"codex"}, "claude")
	if got != "claude" || same {
		t.Fatalf("got %q same=%v, want claude false", got, same)
	}
}

// With only the worker's provider available the role falls back to it and
// says so.
func TestPickIndependentProviderFallbackIsReported(t *testing.T) {
	got, same := pickIndependentProvider([]string{"codex"}, []string{"codex"}, "codex")
	if got != "codex" || !same {
		t.Fatalf("got %q same=%v, want codex true", got, same)
	}
	if !sameProviderAsWorker("Codex", "codex") || sameProviderAsWorker("claude", "codex") {
		t.Fatal("sameProviderAsWorker mismatch")
	}
}
