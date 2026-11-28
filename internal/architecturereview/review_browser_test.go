package architecturereview

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReviewPageRendersInHeadlessChromium serves the real page and confirms a
// browser renders the diagram, makes a part focusable, and paints the chosen
// builder badge. It needs a Chromium binary; CI has none, so it skips unless
// MARSHAL_BROWSER_CHECK names one. The picker needs a click, so this only
// checks the rendered diagram and badge.
func TestReviewPageRendersInHeadlessChromium(t *testing.T) {
	path := os.Getenv("MARSHAL_BROWSER_CHECK")
	if path == "" {
		t.Skip("set MARSHAL_BROWSER_CHECK to a Chromium path to run the headless render check")
	}
	builders := func(context.Context) []BuilderOption {
		return []BuilderOption{
			{ID: "claude", Glyph: "✳", Available: true, Limit: "5h 62% left · resets 14:05"},
			{ID: "codex", Glyph: "⬡", Available: false, Reason: "not installed"},
			{ID: "agy", Glyph: "◆", Available: true},
			{ID: "opencode", Glyph: "●", Available: true},
		}
	}
	r := builderReview(t, filepath.Join(t.TempDir(), "state"), builders, nil)
	if err := applyFixture(t, r, builderRequest(t, r, "capture", "claude")); err != nil {
		t.Fatal(err)
	}
	token := "fixture-token-with-more-than-sixteen-characters"
	srv := httptest.NewServer(r.Handler(token))
	defer srv.Close()

	cmd := exec.Command(path, "--headless=new", "--disable-gpu", "--virtual-time-budget=4000", "--dump-dom", srv.URL+"/?token="+token)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chromium dump failed: %v\n%s", err, out)
	}
	dom := string(out)
	for _, want := range []string{`data-id="capture"`, `tabindex="0"`, "✳ claude", "rev 0 → 1 · proposed"} {
		if !strings.Contains(dom, want) {
			t.Errorf("dumped DOM does not contain %q\n%s", want, dom)
		}
	}
}

// Exercise the shipped JavaScript with the renderer option still present when
// the page consumes its token, and check the compound SVG arrives asynchronously.
func TestRoomyReviewInHeadlessChromium(t *testing.T) {
	browser := os.Getenv("MARSHAL_BROWSER_CHECK")
	if browser == "" {
		t.Skip("set MARSHAL_BROWSER_CHECK to run the browser check")
	}
	r := builderReview(t, filepath.Join(t.TempDir(), "state"), nil, nil)
	token := "fixture-token-with-more-than-sixteen-characters"
	handler := r.Handler(token)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Path != "/" {
			handler.ServeHTTP(w, q)
			return
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, q)
		for k, values := range recorder.Header() {
			for _, value := range values {
				w.Header().Add(k, value)
			}
		}
		io.WriteString(w, recorder.Body.String()+`<script>const probe=setInterval(()=>{const n=document.getElementById('roomy-node-capture'),svg=document.getElementById('roomy-svg'),host=document.getElementById('roomy-canvas');if(!n)return;clearInterval(probe);n.dispatchEvent(new MouseEvent('click',{bubbles:true}));document.body.dataset.roomySelected=String(!document.getElementById('editor').hidden);document.body.dataset.roomyNatural=String(Math.abs(svg.getBoundingClientRect().width-Number(svg.getAttribute('width')))<1);document.body.dataset.roomyScroll=String(host.scrollHeight>host.clientHeight&&host.scrollWidth>host.clientWidth);},50);</script>`)
	}))
	defer srv.Close()
	cmd := exec.Command(browser, "--headless=new", "--disable-gpu", "--window-size=320,300", "--virtual-time-budget=4000", "--dump-dom", srv.URL+"/?token="+token+"&layout=roomy")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser failed: %v\n%s", err, out)
	}
	dom := string(out)
	for _, want := range []string{`id="roomy-canvas"`, `id="roomy-svg"`, `id="roomy-node-capture"`, `overflow: auto`, `max-width: none`, `data-roomy-selected="true"`, `data-roomy-natural="true"`, `data-roomy-scroll="true"`} {
		if !strings.Contains(dom, want) {
			t.Errorf("compound browser drawing missing %q", want)
		}
	}
}
