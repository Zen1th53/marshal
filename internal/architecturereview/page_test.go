package architecturereview

import (
	"regexp"
	"strings"
	"testing"
)

// enTableValue extracts the page's const EN={…}; fallback table and parses its
// key:'text' pairs with the exact pattern the task fixes.
func enTablePairs(t *testing.T) map[string]string {
	t.Helper()
	table := regexp.MustCompile(`const EN=\{(.*?)\};`).FindStringSubmatch(reviewHTML)
	if table == nil {
		t.Fatal("review.html has no const EN={…}; fallback table")
	}
	pairs := regexp.MustCompile(`([a-z_]+):'((?:[^'\\]|\\.)*)'`).FindAllStringSubmatch(table[1], -1)
	out := map[string]string{}
	for _, pair := range pairs {
		out[pair[1]] = pair[2]
	}
	return out
}

func TestPageFallbackTableMatchesEnglishStrings(t *testing.T) {
	table := enTablePairs(t)
	english := EnglishUIStrings()
	if len(table) != len(english) {
		t.Fatalf("EN table has %d keys, EnglishUIStrings has %d", len(table), len(english))
	}
	for key, want := range english {
		got, ok := table[key]
		if !ok {
			t.Errorf("EN table is missing key %q", key)
			continue
		}
		if got != want {
			t.Errorf("EN[%q] = %q, want %q", key, got, want)
		}
	}
	for key := range table {
		if _, ok := english[key]; !ok {
			t.Errorf("EN table has unknown key %q", key)
		}
	}
}

func TestPageUsesOnlyKnownStringKeys(t *testing.T) {
	english := EnglishUIStrings()
	for _, match := range regexp.MustCompile(`\bt\('([a-z_]+)'\)`).FindAllStringSubmatch(reviewHTML, -1) {
		if _, ok := english[match[1]]; !ok {
			t.Errorf("review.html calls t(%q), which is not an English UI string key", match[1])
		}
	}
}

func TestPagePartPanelContract(t *testing.T) {
	for _, want := range []string{
		`id="builder-options" role="radiogroup"`,
		`input.type='radio'`,
		`/api/builders`,
		`action:'builder'`,
		`g.setAttribute('tabindex','0')`,
	} {
		if !strings.Contains(reviewHTML, want) {
			t.Errorf("review.html does not contain %q", want)
		}
	}
	if strings.Contains(reviewHTML, "innerHTML") {
		t.Error("review.html must not use innerHTML")
	}
}
