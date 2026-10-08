package tui

import (
	"context"
	"strings"
	"testing"
)

func TestAcceptanceWelcomeAndHelpLeadWithMarshal(t *testing.T) {
	ws := NewWorkspace(nil, "project", "session")
	header := buildHeader(UIState{}, ws.theme, 80)
	if !strings.Contains(header[0], "Talk to the Marshal: /marshal chat") {
		t.Fatalf("first screen line: %v", header)
	}
	lines := activitySection(UIState{}, ws.theme, 80)
	if !strings.Contains(lines[0], "/marshal chat") {
		t.Fatalf("first guidance: %v", lines)
	}
	for _, command := range []string{"/help", "/?"} {
		out, err := ws.ExecuteCommand(context.Background(), command)
		ws.state.LastOutput, ws.state.LastCommand = out, command
		frame := BuildFrame(ws.state, ws.theme, ws.workDir, ws.composer, nil, 80, 24)
		visible, _ := frame.Lines(80, 24)
		if !strings.Contains(strings.Join(visible, "\n"), "Talk to the Marshal: /marshal chat") {
			t.Fatalf("80x24 help hides the first command: %v", visible)
		}
		if err != nil || !strings.Contains(strings.Split(out, "\n")[0], "/marshal chat") || !strings.Contains(out, "/help all") || len(strings.Split(out, "\n")) > 14 || strings.Contains(out, "UNKNOWN") {
			t.Fatalf("overview %s: %s (%v)", command, out, err)
		}
	}
	out, err := ws.ExecuteCommand(context.Background(), "/help all")
	if err != nil || !strings.Contains(out, "/goal constraints") {
		t.Fatalf("full reference: %s (%v)", out, err)
	}
}
