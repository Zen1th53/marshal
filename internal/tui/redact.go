package tui

import (
	"strings"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/redaction"
)

// Recognise old visible kickoffs and quoted protocol excerpts before any
// truncation. Whole records are withheld so wrapped or escaped copies cannot
// leave the remainder of the instructions on screen or in peer briefings.
var marshalProtocolLines = func() []string {
	protocol, _ := constitution.MarshalProtocol()
	var lines []string
	for _, line := range strings.Split(protocol, "\n") {
		if len(strings.TrimSpace(line)) >= 60 {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}()

func containsMarshalProtocol(input string) bool {
	if strings.Contains(input, "MARSHAL PROTOCOL") {
		return true
	}
	for _, line := range marshalProtocolLines {
		if strings.Contains(input, line) {
			return true
		}
	}
	return false
}

func hideMarshalProtocol(input string) string {
	if containsMarshalProtocol(input) {
		return "[REDACTED]"
	}
	return input
}

// RedactContent scrubs sensitive content with the shared project helper.
func RedactContent(input string, knownSecrets []string) string {
	return redaction.RedactContent(hideMarshalProtocol(input), knownSecrets)
}
