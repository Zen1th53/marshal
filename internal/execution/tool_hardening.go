package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	// Regex matching ANSI escape sequences (CSI sequences)
	ansiCsiRegex = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	// Regex matching OSC (Operating System Command) escape sequences: \x1b] ... (\x07|\x1b\\)
	ansiOscRegex = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)
	// Regex matching other 2-character escape codes
	ansiOtherRegex = regexp.MustCompile(`\x1b[@-Z\\-_]`)
)

const (
	MaxInlineSummaryBytes = 64 * 1024       // 64 KB
	MaxToolOutputLineLen  = 4096            // 4 KB max per line
	MaxRawCapturedBytes   = 2 * 1024 * 1024 // 2 MB
	MaxToolOutputBytes    = 100 * 1024      // 100 KB bounded log threshold
)

// StripAndBoundLogs sanitizes untrusted log text, strips ANSI/escape sequences, and bounds size.
func StripAndBoundLogs(input string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = MaxToolOutputBytes
	}
	summary, _, _ := SanitizeToolOutput([]byte(input), maxBytes)
	return summary
}

// StripEscapeSequences removes ANSI CSI, OSC, and control escape sequences.
func StripEscapeSequences(input string) string {
	cleaned := ansiOscRegex.ReplaceAllString(input, "")
	cleaned = ansiCsiRegex.ReplaceAllString(cleaned, "")
	cleaned = ansiOtherRegex.ReplaceAllString(cleaned, "")
	return cleaned
}

// SanitizeToolOutput cleanses untrusted output: strips escapes, invalid UTF-8, and bounds line lengths.
func SanitizeToolOutput(raw []byte, maxBytes int) (summary string, fullDigest string, truncated bool) {
	if maxBytes <= 0 {
		maxBytes = MaxInlineSummaryBytes
	}

	fullSum := sha256.Sum256(raw)
	fullDigest = hex.EncodeToString(fullSum[:])

	if len(raw) > maxBytes {
		raw = raw[:maxBytes]
		truncated = true
	}

	// Ensure valid UTF-8
	validStr := strings.ToValidUTF8(string(raw), "")
	cleanStr := StripEscapeSequences(validStr)

	// Filter control characters (keep \n, \t, \r)
	var buf strings.Builder
	buf.Grow(len(cleanStr))
	for _, r := range cleanStr {
		if r == '\n' || r == '\t' || r == '\r' || (r >= 32 && r != 127) {
			buf.WriteRune(r)
		}
	}

	// Bound individual line lengths
	lines := strings.Split(buf.String(), "\n")
	for i, line := range lines {
		if len(line) > MaxToolOutputLineLen {
			lines[i] = line[:MaxToolOutputLineLen] + " [LINE_TRUNCATED]"
		}
	}
	summary = strings.Join(lines, "\n")
	if truncated {
		summary += "\n[OUTPUT_TRUNCATED_TO_BOUND]"
	}

	summary = RedactSecrets(summary)
	return summary, fullDigest, truncated
}

// DetectHostileLogInjection inspects output for fake system prompt headers or control injections.
func DetectHostileLogInjection(text string) (bool, string) {
	lower := strings.ToLower(text)
	dangerousPrefixes := []string{
		"system: override",
		"ignore all previous instructions",
		"marshal_override:",
		"admin_approval_bypass:",
		"<|im_start|>",
		"<|im_end|>",
	}

	for _, p := range dangerousPrefixes {
		if strings.Contains(lower, p) {
			return true, fmt.Sprintf("detected prompt injection pattern %q in tool output", p)
		}
	}
	return false, ""
}

// ComputeArtifactDigest computes a SHA256 hex digest of a byte slice.
func ComputeArtifactDigest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// SafeStringPreview returns a truncated, single-line sanitized string safe for terminal rendering.
func SafeStringPreview(s string, maxLen int) string {
	stripped := StripEscapeSequences(s)
	oneLine := strings.ReplaceAll(stripped, "\n", " ")
	oneLine = strings.ReplaceAll(oneLine, "\r", " ")
	if !utf8.ValidString(oneLine) {
		oneLine = strings.ToValidUTF8(oneLine, "")
	}
	if len(oneLine) > maxLen && maxLen > 0 {
		return oneLine[:maxLen] + "..."
	}
	return oneLine
}
