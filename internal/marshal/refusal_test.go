package marshal

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeRefusalTextTruncatesOnUTF8RuneBoundary(t *testing.T) {
	tests := []struct {
		name   string
		prefix int
		ch     string
	}{
		{"two-byte-rune-straddling", maxRefusalTextBytes - 1, "é"},
		{"three-byte-rune-straddling-1", maxRefusalTextBytes - 1, "世"},
		{"three-byte-rune-straddling-2", maxRefusalTextBytes - 2, "世"},
		{"four-byte-rune-straddling-1", maxRefusalTextBytes - 1, "🚀"},
		{"four-byte-rune-straddling-2", maxRefusalTextBytes - 2, "🚀"},
		{"four-byte-rune-straddling-3", maxRefusalTextBytes - 3, "🚀"},
		{"all-multibyte", 0, strings.Repeat("🚀", maxRefusalTextBytes)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := strings.Repeat("a", tc.prefix) + tc.ch + "tail"
			got := SanitizeRefusalText(in)
			if !utf8.ValidString(got) {
				t.Fatalf("SanitizeRefusalText returned invalid UTF-8: %x", got)
			}
			if len(got) > maxRefusalTextBytes {
				t.Fatalf("len(got) = %d, want <= %d", len(got), maxRefusalTextBytes)
			}
			if tc.prefix > 0 && got != strings.Repeat("a", tc.prefix) {
				t.Fatalf("got %q (len %d), want %d 'a' bytes without partial rune", got, len(got), tc.prefix)
			}
			if tc.prefix == 0 && len(got) != maxRefusalTextBytes {
				t.Fatalf("all-multibyte len = %d, want %d", len(got), maxRefusalTextBytes)
			}
		})
	}
}
