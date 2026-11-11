//go:build !governed

package tui

import "github.com/Zen1th53/marshal/internal/cloud"

const ultraSwitchable = false
const ultraFixedAnswer = "Everything is already on in this build; there is nothing to switch."

func buildUltraStatus(gate *cloud.Gate, execution bool) string {
	if !gate.Entitled() {
		return ""
	}
	if _, leased := gate.ExpiresAt(); leased {
		return ""
	}
	if execution {
		return "ULTRA: ACTIVE (Marshal build, no cloud check)"
	}
	return "ULTRA: ENTITLED, EXECUTION OFF (Marshal build, no cloud check)"
}
