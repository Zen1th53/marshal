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
