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
	s := &MarshalService{ModelProvider: "codex", IndependentVerify: func(context.Context, marshal.Run, string, verification.Session) error { return nil }}
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "codex", nil }
	if err := s.requireIndependentVerifier(context.Background(), marshal.Run{Tier: marshal.Ultra}); err != nil {
		t.Fatalf("a single provider could not verify: %v", err)
	}
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "", nil }
	if err := s.requireIndependentVerifier(context.Background(), marshal.Run{Tier: marshal.Ultra}); err == nil {
		t.Fatal("a missing verifier was accepted")
	}
}
