//go:build !governed

package tui

// securityEditionInfo is the public Marshal's security: workers run on the
// host as the user, each task in its own Git worktree, with the provider
// CLIs' own sign-in. Nothing here claims a sandbox or broker it does not run.
func securityEditionInfo() securityEdition {
	return securityEdition{
		Mode:       "Marshal",
		Sandbox:    "none · workers run on this machine as you; each task has its own git worktree",
		Secrets:    "workers use their provider CLI's own sign-in; Marshal redacts secrets from its records",
		Headline:   "host execution, review before merge",
		EgressNote: "not enforced: workers have your network access; requests are listed when recorded",
		LeaseNote:  "not used by host workers",
	}
}
