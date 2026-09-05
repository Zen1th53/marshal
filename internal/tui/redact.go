package tui

import (
	"regexp"

	"github.com/Zen1th53/marshal/internal/auth"
)

var (
	bearerPattern = regexp.MustCompile(`(?i)Bearer\s+[a-zA-Z0-9_\-\.]{10,}`)
	skPattern     = regexp.MustCompile(`sk-[a-zA-Z0-9_\-]{16,}`)
	keyPattern    = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password)\s*[:=]\s*["']?([^"'\s]{8,})["']?`)
)

// RedactContent scrubs sensitive tokens, keys, and patterns from text before rendering in TUI.
func RedactContent(input string, knownSecrets []string) string {
	if len(knownSecrets) > 0 {
		b := auth.RedactSecrets([]byte(input), knownSecrets)
		input = string(b)
	}

	input = bearerPattern.ReplaceAllString(input, "Bearer [REDACTED]")
	input = skPattern.ReplaceAllString(input, "sk-[REDACTED]")
	input = keyPattern.ReplaceAllString(input, "$1: [REDACTED]")

	return input
}
