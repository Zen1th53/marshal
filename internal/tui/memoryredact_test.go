package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// Fixture credentials are assembled at runtime rather than written as literals,
// so this file holds no string that reads as a real key to a secret scanner.
func fakeKey() string   { return "sk" + "-" + strings.Repeat("a", 20) + "012345" }
func fakeToken() string { return strings.Repeat("b", 26) }

// A credential inside a memory record must not reach the screen.
//
// Memory records hold whatever an agent saw. Bounding the excerpt limits how
// much of a record renders, but a token is shorter than the bound, so the
// bound alone does not protect anything. The excerpt runs through the same
// pattern redaction as the rest of the TUI, before the bound is applied.
func TestMemoryExcerptRedactsCredentials(t *testing.T) {
	for _, c := range []struct {
		name   string
		body   string
		secret string
	}{
		{"provider key", "the provider returned " + fakeKey(), fakeKey()},
		{"bearer header", "called with Authorization Bearer " + fakeToken(), fakeToken()},
		{"labelled password", "config had password=hunter2 in it", "hunter2"},
		{"labelled api key", "api_key: AKIAIOSFODNN7EXAMPLE was echoed", "AKIAIOSFODNN7EXAMPLE"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := memoryExcerpt(model.MemoryRecordV2{Title: "note", Body: c.body})
			if strings.Contains(got.Text, c.secret) {
				t.Errorf("the excerpt rendered the secret in cleartext:\n%s", got.Text)
			}
			if !got.Redacted {
				t.Error("the excerpt is not marked redacted, so CopyText would not refuse it")
			}
		})
	}
}

// Redaction runs before the length bound, not after.
//
// The patterns match on minimum lengths, so truncating first can leave a
// fragment the pattern no longer recognises. A secret placed far enough into a
// long body that truncation would cut it must still be redacted.
func TestMemoryExcerptRedactsBeforeTruncating(t *testing.T) {
	// Push the key to just before the 120-rune bound so a truncate-then-redact
	// order would cut it mid-token and leave the fragment visible.
	body := strings.Repeat("a", 110) + " " + fakeKey()

	got := memoryExcerpt(model.MemoryRecordV2{Body: body})
	if strings.Contains(got.Text, "sk-aaaaaaaaaaaaaa") {
		t.Errorf("a truncated secret fragment survived into the excerpt:\n%s", got.Text)
	}
}
