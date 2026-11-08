//go:build governed

package startup

const (
	networkReadySummary = "Outbound access is limited to approved destinations."
	ultraModesBody      = "Standard is the default and is fully functional. " +
		"ULTRA adds depth and parallelism and requires a valid entitlement. " +
		"It is never switched on automatically, and it never removes a safety requirement that applies in Standard."
	ultraNotEntitledRemedy = "ULTRA needs a valid entitlement. Standard mode is unaffected."
	ultraExpiredRemedy     = "The ULTRA entitlement has expired. Renew it to use ULTRA again."
)
