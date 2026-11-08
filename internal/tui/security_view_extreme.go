//go:build !governed

package tui

// securityEditionInfo is the Marshal's security: local ULTRA, the
// sandbox per lane with the host fallback the edition allows, and the
// broker's short credential leases.
func securityEditionInfo() securityEdition {
	return securityEdition{
		Mode:    "Marshal · local ULTRA",
		Sandbox: "bwrap per lane · host fallback only when bwrap is missing",
		Secrets: "workers never see auth files; the broker issues short leases",
	}
}
