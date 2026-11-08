package app

import "github.com/Zen1th53/marshal/internal/opencodecli"

// sameProbedCLIVersion compares the installed CLI's --version line with the
// version its adapter probe reported. The OpenCode adapter reports the
// normalized version ("2.1.4") of the raw line ("opencode v2.1.4"); every
// other adapter reports the raw line itself.
func sameProbedCLIVersion(provider, installed, probed string) bool {
	if installed == probed {
		return true
	}
	if provider == "opencode" {
		normalized := opencodecli.NormalizeVersion(installed)
		return normalized != "" && normalized == probed
	}
	return false
}
