package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/doctor"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/usage"
)

// installPickerStubs puts an executable stub for each name on PATH, so
// installedNeutralProviders counts it as installed without running it.
func installPickerStubs(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\necho 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// fakePickerProviders is a providerViewSource whose overview is fixed.
type fakePickerProviders struct {
	ps  []store.ProviderStatus
	err error
}

func (f fakePickerProviders) ProviderOverview(context.Context) ([]store.ProviderStatus, error) {
	return f.ps, f.err
}
func (fakePickerProviders) VerifyHarness(context.Context, string, string) (string, error) {
	return "", nil
}
func (fakePickerProviders) RouteTask(context.Context, string, string) (string, error) {
	return "", nil
}

// quotaLeft is a known quota with one window that has pct percent left,
// resetting at reset.
func quotaLeft(provider string, pct float64, reset time.Time) usage.Quota {
	return usage.Quota{
		Provider: provider,
		Known:    true,
		Windows:  []usage.Window{{Label: "5h", LeftPercent: pct, ResetsAt: reset}},
	}
}

// quotaUnknown is a provider whose limit cannot be read.
func quotaUnknown(provider string) usage.Quota {
	return usage.Quota{Provider: provider, Reason: usage.NotExposed}
}

// pickerWorkspace builds a workspace rooted at a temp dir, with the given
// usage quotas and an empty overview, for the decide tests.
func pickerWorkspace(t *testing.T, quotas ...usage.Quota) *Workspace {
	t.Helper()
	w := &Workspace{workDir: t.TempDir()}
	w.provSource = fakePickerProviders{}
	qs := append([]usage.Quota(nil), quotas...)
	w.usage.readFn = func(time.Time) []usage.Quota { return qs }
	return w
}

func TestPickerSkippedForReturningUser(t *testing.T) {
	installPickerStubs(t, "codex", "claude")
	reset := time.Now().Add(3 * time.Hour)
	w := pickerWorkspace(t, quotaLeft("codex", 42, reset), quotaLeft("claude", 94, reset))
	if err := saveDefaultProvider(w.providerRoot(), "codex"); err != nil {
		t.Fatal(err)
	}
	if w.decideMarshalPicker(t.Context()) {
		t.Fatal("picker opened for a saved provider with quota left")
	}
	if w.pickerOpen() {
		t.Fatal("picker is open")
	}
}

func TestPickerShownWhenSavedOutOfLimit(t *testing.T) {
	installPickerStubs(t, "codex", "claude")
	reset := time.Now().Add(3 * time.Hour)
	w := pickerWorkspace(t, quotaLeft("codex", 0, reset), quotaLeft("claude", 94, reset))
	if err := saveDefaultProvider(w.providerRoot(), "codex"); err != nil {
		t.Fatal(err)
	}
	if !w.decideMarshalPicker(t.Context()) {
		t.Fatal("picker did not open for a saved provider out of limit")
	}
	pv := w.pickerPaint()
	if !strings.HasPrefix(pv.reason, "codex is out of limit") {
		t.Fatalf("reason = %q, want it to start with %q", pv.reason, "codex is out of limit")
	}
	// 2 picks the second candidate (claude) and saves it.
	if !w.handlePickerKey(KeyEvent{Type: KeyRune, Rune: '2'}) {
		t.Fatal("picker did not take the digit key")
	}
	if got := loadDefaultProvider(w.providerRoot()); got != "claude" {
		t.Fatalf("saved provider = %q, want claude", got)
	}
	if w.pickerOpen() {
		t.Fatal("picker still open after confirm")
	}
}

func TestPickerUnknownLimitIsUnavailable(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false), now: time.Now()}
	v.pick = pickerView{cands: []pickerCand{{provider: "claude", known: false}}, sel: 0}
	first := StripANSI(v.pickerBody(120, 6, 0)[0])
	if !strings.Contains(first, "claude unavailable") {
		t.Fatalf("first line = %q, want it to show claude unavailable", first)
	}
	if strings.Contains(first, "%") {
		t.Fatalf("first line shows a percent for an unknown limit: %q", first)
	}
}

func TestPickerDigitPicksAndSaves(t *testing.T) {
	installPickerStubs(t, "codex", "claude")
	reset := time.Now().Add(3 * time.Hour)
	// No saved provider: a first-run owner sees the picker.
	w := pickerWorkspace(t, quotaLeft("codex", 50, reset), quotaLeft("claude", 94, reset))
	if !w.decideMarshalPicker(t.Context()) {
		t.Fatal("picker did not open for a first-run owner")
	}
	if !w.handlePickerKey(KeyEvent{Type: KeyRune, Rune: '1'}) {
		t.Fatal("picker did not take the digit key")
	}
	path := filepath.Join(w.providerRoot(), ".marshal", "default-provider")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "codex" {
		t.Fatalf("file holds %q, want codex", got)
	}
}

func TestPickerEnterRefusesOutOfLimit(t *testing.T) {
	w := &Workspace{workDir: t.TempDir()}
	w.openPicker([]pickerCand{{provider: "codex", known: true, out: true, until: time.Now().Add(time.Hour)}}, 0, "")
	if w.handlePickerKey(KeyEvent{Type: KeyEnter}); !w.pickerOpen() {
		t.Fatal("Enter on an out-of-limit entry closed the picker")
	}
	if _, err := os.Stat(filepath.Join(w.providerRoot(), ".marshal", "default-provider")); !os.IsNotExist(err) {
		t.Fatalf("Enter on an out-of-limit entry saved a provider: %v", err)
	}
}

func TestPickerSingleCandidateSkips(t *testing.T) {
	installPickerStubs(t, "codex")
	w := pickerWorkspace(t, quotaLeft("codex", 50, time.Now().Add(3*time.Hour)))
	if w.decideMarshalPicker(t.Context()) {
		t.Fatal("picker opened for a single usable candidate")
	}
	if got := loadDefaultProvider(w.providerRoot()); got != "codex" {
		t.Fatalf("single candidate was not saved: %q", got)
	}
}

func TestPickerTextShape(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false), now: time.Now()}
	v.pick = pickerView{
		cands: []pickerCand{
			{provider: "codex", left: 42, known: true},
			{provider: "claude", left: 94, known: true},
		},
		sel: 0,
	}
	first := strings.TrimRight(StripANSI(v.pickerBody(120, 6, 0)[0]), " ")
	want := "Who plans?  ⬡ codex 42% · ✳ claude 94%"
	if first != want {
		t.Fatalf("first line = %q, want %q", first, want)
	}
}

func TestPickerHoldsChatStartUntilConfirm(t *testing.T) {
	root := newLaunchRepo(t)
	installPickerStubs(t, "codex", "claude")
	orig := launchDoctor
	launchDoctor = func(context.Context, string, doctor.Options) doctor.Report { return passReport() }
	defer func() { launchDoctor = orig }()

	reset := time.Now().Add(3 * time.Hour)
	w := &Workspace{workDir: root}
	w.provSource = fakePickerProviders{}
	w.usage.readFn = func(time.Time) []usage.Quota {
		return []usage.Quota{quotaLeft("codex", 0, reset), quotaLeft("claude", 94, reset)}
	}
	if err := saveDefaultProvider(root, "codex"); err != nil {
		t.Fatal(err)
	}

	ran := false
	w.afterLaunchCheck(func() { ran = true })
	w.runLaunchCheck(t.Context())

	if !w.pickerOpen() {
		t.Fatal("picker did not open after a ready check with the saved provider out of limit")
	}
	if ran {
		t.Fatal("chat start ran before the picker was answered")
	}
	w.confirmPicker(1) // claude
	if !ran {
		t.Fatal("chat start did not run after the picker was confirmed")
	}
}
