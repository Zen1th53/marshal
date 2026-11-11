//go:build governed

package tui

import "github.com/Zen1th53/marshal/internal/cloud"

const ultraSwitchable = true
const ultraFixedAnswer = ""

func buildUltraStatus(*cloud.Gate, bool) string { return "" }
