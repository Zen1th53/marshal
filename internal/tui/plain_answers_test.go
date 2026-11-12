package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// /status says once, plainly, whether anonymous telemetry is on, with or
// without a run.
func TestStatusSaysWhetherTelemetryIsOn(t *testing.T) {
	for _, line := range []string{app.TelemetryOnLine, app.TelemetryOffLine} {
		old := telemetryLineFor
		telemetryLineFor = func(string) string { return line }
		ws := NewWorkspace(nil, "project", "session")
		out, err := ws.ExecuteCommand(t.Context(), "/status")
		telemetryLineFor = old
		ws.Close()
		if err != nil || !strings.HasSuffix(out, "\n"+line) || strings.Count(out, "anonymous telemetry") != 1 {
			t.Fatalf("/status = %q, %v; want it to end with %q", out, err, line)
		}
	}
}

// The Board help's footer says whether anonymous telemetry is on, and holds
// still while the keys scroll.
func TestBoardHelpFooterSaysWhetherTelemetryIsOn(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	v := boardViewVisual(memEpoch, th)
	l := layout.Compute(80, 24, layout.Split)
	r := l.Overlay(0)
	plain, plainMax, _ := v.helpOverlay(r.W, r.H, 0)
	v.telemetry = app.TelemetryOnLine
	for _, scroll := range []int{0, 5} {
		help, most, _ := v.helpOverlay(r.W, r.H, scroll)
		if len(help) != len(plain) || most != plainMax+1 {
			t.Fatalf("footer changed the box: %d lines (want %d), max %d (want %d)", len(help), len(plain), most, plainMax+1)
		}
		footer := StripANSI(help[len(help)-2])
		if !strings.Contains(footer, app.TelemetryOnLine) {
			t.Fatalf("scroll %d: footer %q lacks the telemetry line", scroll, footer)
		}
	}
	for _, line := range plain {
		if strings.Contains(StripANSI(line), "telemetry") {
			t.Fatalf("help without a telemetry line shows one: %q", line)
		}
	}
}

// A command that takes nothing says so plainly when given something.
func TestZeroArgumentUsageIsPlain(t *testing.T) {
	ws := NewWorkspace(nil, "project", "session")
	t.Cleanup(ws.Close)
	for _, cmd := range []string{"/claims", "/agents", "/why"} {
		out, err := ws.cmd.Handle(t.Context(), cmd+" extra")
		if err != nil || out != "Usage: "+cmd+" — type it on its own, with nothing after it." {
			t.Fatalf("%s extra = %q, %v", cmd, out, err)
		}
	}
}

// A successful /harness verify, whoever ran it, marks the provider verified
// on the Board at once: the first-use card and the header's provider dots
// read the new profile without the Providers view being opened.
func TestHarnessVerifyMarksProviderVerifiedEverywhere(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-verify-everywhere", false)
	dir := t.TempDir()
	stub := `#!/bin/sh
case "$1" in
--version) printf 'native-probe-version\n'; exit 0;;
--help) printf '%s\n' '--print --output-format --model --mode --conversation --sandbox --new-project --disable-slash-commands'; exit 0;;
esac
printf '%s\n' '{"status":"SUCCESS","conversation_id":"native-message","response":"MARSHAL_PROBE_OK"}'
`
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	w.loadFirstUse(t.Context())
	state := func() string {
		for _, c := range w.firstUseChips(time.Now()) {
			if c.name == "agy" {
				return c.state
			}
		}
		return ""
	}
	if got := state(); got == "verified" {
		t.Fatalf("agy verified before any verify: %s", got)
	}
	// The Marshal's command reaches the same handler the owner types.
	if _, err := w.cmd.Handle(t.Context(), "/harness verify-native agy"); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != "verified" {
		t.Fatalf("first-use card says %q after a successful verify", got)
	}
	if dot := w.providerDots(time.Now())["agy"]; dot != "●" {
		t.Fatalf("header dot %q after a successful verify", dot)
	}
}
