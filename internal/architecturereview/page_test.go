package architecturereview

import (
	"os/exec"
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

func TestPageAcceptContract(t *testing.T) {
	for _, want := range []string{
		`id="accept-summary"`,
		`id="accepted-banner"`,
		`>Accept architecture<`,
		`builders_digest:state.builders_digest`,
		`confirm(t('reopen_prompt'))`,
		`>Accepted — the run continues in your terminal.<`,
		`t('accept_wait_reply')`,
		`t('accept_open_request')`,
		`t('accept_unsent')`,
		`state.accept_blocked`,
	} {
		if !strings.Contains(reviewHTML, want) {
			t.Errorf("review.html does not contain %q", want)
		}
	}
	if strings.Contains(reviewHTML, ">Approve<") {
		t.Error("review.html still labels the button Approve")
	}
}

func TestPageRevisionAndDesignContract(t *testing.T) {
	for _, want := range []string{
		`id="revision-bar"`,
		`id="design-open"`,
		`id="design"`,
		`/api/design`,
		`design_digest:`,
		`class:'changed-badge'`,
	} {
		if !strings.Contains(reviewHTML, want) {
			t.Errorf("review.html does not contain %q", want)
		}
	}
	if strings.Contains(reviewHTML, "innerHTML") {
		t.Error("review.html must not use innerHTML")
	}
}

// Exercise the real page script with both the new seed and a legacy seed.
// The browser smoke test remains available for SVG rendering in Chromium.
func TestPageArchitectureLifecycle(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed for page interaction checks")
	}
	cmd := exec.Command(node, "testdata/lifecycle.cjs", "review.html", "testdata/lifecycle.json")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("page interactions: %v\n%s", err, out)
	}
}

func TestPageArchitectureChangeStyles(t *testing.T) {
	// Change kinds stay legible: current is solid neutral, this goal's
	// additions are solid accent (never dashed noise), removals fade and are
	// struck through, changes carry an accent outline. Each also has a text
	// tag, so meaning never rests on colour alone.
	for _, want := range []string{
		`.edge.new .edge-line{stroke:var(--accent)`,
		`.edge.changed .edge-casing{display:inline}`,
		`.node.changed .change-outline{fill:none;stroke:var(--amber)`,
		`.node.removed,.edge.removed{opacity:.45}`,
		`text-decoration:line-through`,
		`t(status==='new'?'tag_new':'tag_removed')`,
		`id="history-open" aria-expanded="false" aria-controls="history-panel"`,
		`id="only-changes" type="checkbox"`,
	} {
		if !strings.Contains(reviewHTML, want) {
			t.Errorf("review.html does not contain %q", want)
		}
	}
	if strings.Contains(reviewHTML, "stroke-dasharray:7 5") {
		t.Error("review.html still dashes changed lines")
	}
}

// The viewer navigates without libraries: viewBox camera, wheel and pinch
// zoom, drag pan, keyboard, minimap, focus, search and bundled connections.
func TestPageViewerContract(t *testing.T) {
	for _, want := range []string{
		`id="zoom-in"`, `id="zoom-out"`, `id="fit"`, `id="zoom-actual"`, `id="minimap"`, `id="search"`, `id="tip"`,
		`$('diagram').onwheel=`, `pinch=`, `requestAnimationFrame`, `prefers-reduced-motion`, `--bg:#0c121c`, `--accent:#7db1ff`,
		`case'0':fit(true)`, `case'ArrowLeft'`, `bundle-count`, `#diagram.focusing .node:not(.lit)`, `id="incoming"`, `id="outgoing"`, `id="covers"`, `id="advanced"`, `history.pushState`, `globalThis.onpopstate`, `/api/codeview`, `prefers-reduced-motion`, `class:'port'`,
	} {
		if !strings.Contains(reviewHTML, want) {
			t.Errorf("review.html does not contain %q", want)
		}
	}
	page := strings.ReplaceAll(reviewHTML, "http://www.w3.org/2000/svg", "")
	for _, banned := range []string{"<script src", "<link", "@import", "http://", "https://", "url(http"} {
		if strings.Contains(page, banned) {
			t.Errorf("review.html loads an external resource: %q", banned)
		}
	}
}
