//go:build governed

package tui

// Community keeps each provider's own approval and trust prompts: MARSHAL adds
// no tool pre-approval, approval policy or project trust to its launches. The
// session server it attaches here is token-scoped; whether its tools may run
// without a prompt stays the operator's decision in the provider itself.
func applyProviderLaunchPolicy(_ string, _ string, args, env []string, _ string) ([]string, []string, error) {
	return args, env, nil
}

func claudeMarshalToolAllowFlags() []string { return nil }

// codexMarshalConfineEnforced is false here: approvals stay the operator's,
// so a Codex write outside the Marshal's directories asks the person rather
// than being refused outright.
const codexMarshalConfineEnforced = false

// claudeMarshalConfineFlags keep every Claude Code edit behind the person's
// own approval: "manual" overrides an acceptEdits, auto or bypass default.
// Community pre-allows no tool, so a rule that let the Marshal write even its
// own directories unasked is not added.
func claudeMarshalConfineFlags() ([]string, bool) {
	return []string{"--permission-mode", "manual"}, false
}
