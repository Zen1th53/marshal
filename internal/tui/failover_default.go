//go:build governed

package tui

import (
	"github.com/Zen1th53/marshal/internal/marshal"
)

func formatFailoverSetting(_ marshal.Settings) string {
	return ""
}

func handleFailoverSetting(_ []string, _ *marshal.Settings) (string, bool, error) {
	return "", false, nil
}
