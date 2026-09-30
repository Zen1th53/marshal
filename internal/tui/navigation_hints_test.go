package tui

import (
	"strings"
	"testing"
)

func TestNavigationHintsFollowAvailability(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	h := &CommandHandler{ws: ws}
	for _, released := range []bool{false, true} {
		for _, entitled := range []bool{false, true} {
			ws.navReleased = released
			ws.AttachULTRA(nil, false)
			if entitled {
				entitleULTRA(t, ws)
			}
			want := released && entitled
			help := h.helpText()
			if strings.Contains(help, "Ctrl+N: Navigation") != want {
				t.Fatalf("help navigation hint with released=%v entitled=%v: %s", released, entitled, help)
			}
			s := ws.GetUIState()
			frame := StripANSI(strings.Join(activitySection(s, NewTheme(ThemeDefault, true, true), 160), "\n"))
			if strings.Contains(frame, "[Esc] Navigation") != want {
				t.Fatalf("activity navigation hint with released=%v entitled=%v: %s", released, entitled, frame)
			}
		}
	}
}
