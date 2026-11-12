//go:build !governed

package startup

// Extreme runs ULTRA locally: there is no entitlement to hold, request or renew.
const (
	ultraModesBody = "Standard is the default and is fully functional. " +
		"ULTRA adds depth and parallelism and runs locally in Marshal, so it needs no entitlement. " +
		"It is never switched on automatically, and it never removes a safety requirement that applies in Standard."
	ultraNotEntitledRemedy = "ULTRA runs locally in Marshal and needs no entitlement. Standard mode is unaffected."
	ultraExpiredRemedy     = "ULTRA runs locally in Marshal and needs no entitlement."
	// ultraReadySummary is what the always-present local ULTRA means for the
	// user, in plain words, for `marshal setup`.
	ultraReadySummary = "Marshal can run several tasks at once, each one reviewed and checked (ULTRA mode)."
)
