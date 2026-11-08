// Package render contains the safe text primitives used for model-facing
// output: path quoting, control/bidi escaping and line-numbered excerpts.
// Repository text is data; these rules keep it from forging envelope
// records (newlines in paths, lines that start like records) or hiding
// content (Trojan Source bidi controls, CVE-2021-42574).
package render

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Version is the renderer version (part of the receipt version vector).
const Version = "text/1"

// IsBidiControl reports Unicode bidirectional formatting characters.
func IsBidiControl(r rune) bool {
	switch r {
	case 0x061C, 0x200E, 0x200F,
		0x202A, 0x202B, 0x202C, 0x202D, 0x202E,
		0x2066, 0x2067, 0x2068, 0x2069:
		return true
	}
	return false
}

// needsEscape reports characters that must never appear raw.
func needsEscape(r rune) bool {
	return r == utf8.RuneError || IsBidiControl(r) || (unicode.IsControl(r) && r != '\t') ||
		r == 0x2028 || r == 0x2029 || r == 0xFEFF || unicode.Is(unicode.Cf, r) && r != 0x200D
}

// EscapeLine escapes control characters (except tab), bidi controls, line
// separators, format characters and invalid UTF-8 in a single line of
// source text. The result never contains a newline.
func EscapeLine(s string) string {
	clean := true
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if needsEscape(r) || (r == utf8.RuneError && n == 1) {
			clean = false
			break
		}
		i += n
	}
	if clean {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case needsEscape(r):
			if r < 0x80 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				fmt.Fprintf(&b, `\u{%04X}`, r)
			}
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// Path renders a repository path. Paths that contain anything but a
// conservative set of printable characters are Go-quoted, so a crafted
// path can never inject a newline or spoofed text into the envelope.
func Path(p string) string {
	for _, r := range p {
		if r > unicode.MaxASCII || r <= ' ' || r == '"' || r == '\\' || r == 0x7f || r == '`' {
			return strconv.QuoteToASCII(p)
		}
	}
	if p == "" {
		return `""`
	}
	return p
}

// Quote renders free text supplied by a caller (e.g. the task) as a
// single-line quoted string.
func Quote(s string) string {
	return strconv.QuoteToGraphic(EscapeLine(strings.ReplaceAll(s, "\n", " ")))
}

// Excerpt renders lines [from, to] (1-based, inclusive) of src with a
// right-aligned line-number prefix "  NN| ". It returns the text and the
// byte interval [start, end) of src covered. Lines longer than maxLine
// bytes are clipped with a marker (0 = no limit).
func Excerpt(src []byte, from, to, maxLine int) (text string, start, end int) {
	if from < 1 {
		from = 1
	}
	var b strings.Builder
	width := len(strconv.Itoa(to)) + 2
	line, pos := 1, 0
	start, end = -1, -1
	for line <= to && pos < len(src) {
		k := bytes.IndexByte(src[pos:], '\n')
		next := len(src)
		l := src[pos:]
		if k >= 0 {
			l = src[pos : pos+k]
			next = pos + k + 1
		}
		if line >= from {
			if start < 0 {
				start = pos
			}
			end = next
			ls := strings.TrimRight(string(l), "\r")
			clipped := ""
			if maxLine > 0 && len(ls) > maxLine {
				cut := maxLine
				for cut > 0 && !utf8.RuneStart(ls[cut]) {
					cut--
				}
				clipped = fmt.Sprintf(" ...[+%d bytes]", len(ls)-cut)
				ls = ls[:cut]
			}
			fmt.Fprintf(&b, "%*d| %s%s\n", width, line, EscapeLine(ls), clipped)
		}
		pos = next
		line++
	}
	if start < 0 {
		start, end = 0, 0
	}
	return b.String(), start, end
}
