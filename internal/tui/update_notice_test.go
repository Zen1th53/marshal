package tui

import (
	"strings"
	"testing"
)

// The notice states the release and the key that installs it, and it is absent
// until a check has actually found one. An empty tag is what a failed or
// disabled check leaves behind, and it must not produce a notice.
func TestActivitySectionShowsAnAvailableUpdate(t *testing.T) {
	th := NewTheme(ThemeNoColor, false, false)

	quiet := strings.Join(activitySection(UIState{}, th, 120), "\n")
	if strings.Contains(quiet, "Update") {
		t.Fatalf("a workspace with no update found showed an update notice:\n%s", quiet)
	}

	noticed := strings.Join(activitySection(UIState{UpdateAvailable: "v9.9.9"}, th, 120), "\n")
	for _, want := range []string{"MARSHAL v9.9.9 is available", "[F10] Download and install", "/update"} {
		if !strings.Contains(noticed, want) {
			t.Fatalf("the update notice does not say %q:\n%s", want, noticed)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(noticed), "Update") {
		t.Fatalf("the notice should lead the activity panel:\n%s", noticed)
	}
}

// A workspace that is busy still shows the notice: it is attached to the panel,
// not to the panel's empty state.
func TestUpdateNoticeSurvivesActivity(t *testing.T) {
	th := NewTheme(ThemeNoColor, false, false)
	state := UIState{
		UpdateAvailable: "v9.9.9",
		ActiveToolCard:  &ToolExecutionCard{Command: "go build ./..."},
	}
	rendered := strings.Join(activitySection(state, th, 120), "\n")
	if !strings.Contains(rendered, "MARSHAL v9.9.9 is available") {
		t.Fatalf("the notice disappeared once the panel had content:\n%s", rendered)
	}
	if !strings.Contains(rendered, "go build ./...") {
		t.Fatalf("the notice replaced the panel's content:\n%s", rendered)
	}
}

// The registry entry is what makes the command discoverable in autocomplete
// and the palette, and it names the key the notice tells the user to press.
func TestUpdateIsAdvertisedWithItsKey(t *testing.T) {
	for _, capability := range GlobalRegistry.All() {
		if capability.TUISurface != "/update" {
			continue
		}
		if capability.KeyboardPath != "F10" {
			t.Fatalf("/update advertises key %q, but the notice tells the user F10", capability.KeyboardPath)
		}
		if capability.CLISurface != "marshal update" {
			t.Fatalf("/update advertises CLI surface %q", capability.CLISurface)
		}
		return
	}
	t.Fatal("the registry does not advertise /update")
}

// F10 installs the release the notice is showing, and checks when there is no
// notice. It must never install something the user was not shown first.
func TestUpdateKeyInstallsOnlyWhatWasOffered(t *testing.T) {
	w := &Workspace{}
	if got := w.updateKeyCommand(); got != "/update" {
		t.Fatalf("with no update found F10 ran %q, want /update", got)
	}
	w.setUpdateAvailable("v9.9.9")
	if got := w.updateKeyCommand(); got != "/update install" {
		t.Fatalf("with an update on screen F10 ran %q, want /update install", got)
	}
	w.setUpdateAvailable("")
	if got := w.updateKeyCommand(); got != "/update" {
		t.Fatalf("after the notice cleared F10 ran %q, want /update", got)
	}
}
