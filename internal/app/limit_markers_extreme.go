//go:build !governed

package app

// Extreme also recognises the quota wording providers print without the
// words "limit" or "quota": HTTP 429, Gemini's RESOURCE_EXHAUSTED and
// OpenCode's exhausted free daily messages, and Claude's "You've hit your
// limit" for its 5-hour window.
var editionLimitMarkers = []string{"(code 429)", " 429 ", "resource_exhausted", "too many requests", "free messages", "hit your limit", "5-hour limit"}
