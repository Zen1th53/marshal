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
// Memory records hold whatever an agent saw. The Memory view's evidence overlay
// (memoryEvidenceLines) is where a record's title and body are shown, and it
// runs every field through the same pattern redaction as the rest of the TUI.
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
			got := strings.Join(memoryEvidenceLines("MEM-1", model.MemoryRecordV2{Title: c.body, Body: c.body}, nil, nil), "\n")
			if strings.Contains(got, c.secret) {
				t.Errorf("the evidence overlay rendered the secret in cleartext:\n%s", got)
			}
		})
	}
}

// Redaction runs over the whole body before it is cut to the shown lines, so a
// secret late in a long record is never left as a visible fragment.
func TestMemoryExcerptRedactsBeforeTruncating(t *testing.T) {
	body := strings.Repeat("a", 110) + " " + fakeKey()

	got := strings.Join(memoryEvidenceLines("MEM-1", model.MemoryRecordV2{Body: body}, nil, nil), "\n")
	if strings.Contains(got, "sk-aaaaaaaaaaaaaa") {
		t.Errorf("a secret fragment survived into the evidence overlay:\n%s", got)
	}
}
