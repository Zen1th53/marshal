package tui

import (
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
)

// A verify whose probe ends at once with nothing to show is, for a provider
// that has no read-only sign-in check (agy), almost always a CLI that was
// never signed in: it exits before answering. Saying "harness probe did not
// produce the exact reply" leaves the owner nothing to do; the sign-in is the
// cause, and the cure is one command.

// verifySignedOut reports whether a failed verify of provider failed for a
// missing sign-in: its text names a sign-in failure by the markers a worker
// failure is classified by, or, for agy, the probe ended without a reply,
// output or limit message.
func verifySignedOut(provider string, err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	if app.IsSignInFailure(text) {
		return true
	}
	if providerWorker(firstNonEmpty(canonicalNeutralProvider(provider), provider)) != "agy" {
		return false
	}
	lower := strings.ToLower(text)
	for _, other := range []string{"provider limit", "timed out", "cancelled", "truncated", "stderr:", "sandbox", "host execution", "modified its worktree", "changed version"} {
		if strings.Contains(lower, other) {
			return false
		}
	}
	switch {
	case strings.Contains(lower, "native agy probe did not produce the exact successful reply"):
		return true
	case strings.Contains(lower, "harness probe did not produce the exact reply"):
		return strings.Contains(lower, "exit code") || strings.Contains(lower, `reply was ""`)
	}
	return false
}

// notSignedInReason is the plain cause and its cure for a provider that is
// not signed in.
func notSignedInReason(provider string) string {
	name := firstNonEmpty(canonicalNeutralProvider(provider), providerWorker(provider), strings.TrimSpace(provider))
	return name + " is not signed in: " + signInHint(name)
}

// signInHint is how to sign a provider in.
func signInHint(provider string) string {
	switch provider {
	case "codex":
		return "run codex login in a terminal"
	case "opencode":
		return "run opencode auth login in a terminal"
	case "claude", "agy":
		return "run " + provider + " once in a terminal and sign in"
	}
	return "sign in to " + provider + " in a terminal"
}
