package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

func TestStatusIsPlainAndMatchesBoard(t *testing.T) {
	old := telemetryLineFor
	telemetryLineFor = func(string) string { return "anonymous telemetry: off" }
	t.Cleanup(func() { telemetryLineFor = old })
	ws := NewWorkspace(nil, "project", "session")
	t.Cleanup(ws.Close)
	ws.setUltraExecution(true)
	ctx := context.Background()

	out, err := ws.ExecuteCommand(ctx, "/status")
	if err != nil {
		t.Fatalf("/status without a run: %v", err)
	}
	want := fmt.Sprintf("Marshal %s · %s\nRun: none yet. Press s and tell the Marshal what to build.\nanonymous telemetry: off", BuildVersion, filepath.Base(ws.providerRoot()))
	if out != want {
		t.Fatalf("no-run status = %q, want %q", out, want)
	}

	board := boardmodel.Board{Run: boardmodel.RunHead{
		Title: "Add login", State: marshal.Dispatching, Done: 1, Total: 3, Running: 2,
	}}
	ws.setMarshalPanel(&MarshalPanel{RunID: "run-status", Board: board})
	out, err = ws.ExecuteCommand(ctx, "/status")
	if err != nil {
		t.Fatalf("/status with a run: %v", err)
	}
	if !strings.Contains(out, "Run: \"Add login\" — running") || !strings.Contains(out, "Tasks: 1 of 3 done · 2 running") {
		t.Fatalf("status is missing Board run facts:\n%s", out)
	}
	if !strings.Contains(out, "Providers: Codex not checked · Claude not checked · Antigravity not checked · OpenCode not checked") {
		t.Fatalf("status is missing the Board provider states:\n%s", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatalf("status contains terminal escape codes: %q", out)
	}
	for _, forbidden := range []string{"CONTROL PLANE", "PROJECT-", "SESSION", "NEEDS_INPUT", "INSTANCE-", "PLAN-"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("status contains forbidden detail %q:\n%s", forbidden, out)
		}
	}

	state := ws.GetUIState()
	p := state.Marshal
	b := p.Board
	b.Inbox = ws.boardInbox()
	v := boardVisual{board: b, now: time.Now(), note: p.Note}
	v.gates = ws.ownerGates(p)
	v.guard, _, _, _ = ws.guardPaint()
	word, _ := v.runWord()
	if !strings.Contains(out, "Run: \"Add login\" — "+StripANSI(v.stateChip())) || !strings.Contains(out, word) {
		t.Fatalf("status state does not match Board state %q:\n%s", word, out)
	}
}
