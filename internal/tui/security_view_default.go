//go:build governed

package tui

// securityEditionInfo is the default edition's security: ULTRA by lease,
// a lane without confinement refused, every credential use the owner's.
func securityEditionInfo() securityEdition {
	return securityEdition{
		Mode:       "standard · ULTRA by lease",
		Sandbox:    "bwrap per lane · a lane that cannot be confined is refused",
		Secrets:    "workers never see auth files; you decide every credential use",
		Headline:   "egress only through the broker",
		EgressNote: "every network call a worker makes in this run",
		LeaseNote:  "short, per task, revoked when the lane ends",
	}
}
