//go:build governed

package tui

import "github.com/Zen1th53/marshal/internal/marshal"

func applyExtremeSetting(s *marshal.Settings, args []string) (bool, string, error) {
	return false, "", nil
}

func formatExtremeSettings(s marshal.Settings) string {
	return ""
}

// effectiveGlobalCap: the default edition has no global lane cap.
func effectiveGlobalCap(marshal.Settings) (int, bool) { return 0, false }

func projectGlobalCap(marshal.Settings) int { return 0 }
