//go:build governed

package tui

// securityEditionInfo is the default edition's security: ULTRA by lease,
// a lane without confinement refused, every credential use the owner's.
func securityEditionInfo() securityEdition {
	return securityEdition{
		Mode:    "standard · ULTRA by lease",
		Sandbox: "bwrap per lane · a lane that cannot be confined is refused",
		Secrets: "workers never see auth files; you decide every credential use",
	}
}
