package tui

import (
	"context"
	"strings"
	"testing"
)

func TestMarshalTypoGuard(t *testing.T) {
	for _, tc := range []struct{ word, suggestion string }{
		{"setings", "settings"}, {"aprove", "approve"}, {"stat", "status"},
		{"SETINGS", "settings"}, {"stauts", "status"}, {"sett", "settings"},
	} {
		t.Run(tc.word, func(t *testing.T) {
			ws := NewWorkspace(nil, "proj", "sess-1")
			got, err := (&CommandHandler{ws: ws}).handleMarshal(context.Background(), []string{tc.word})
			want := "Nothing was run. Did you mean /marshal " + tc.suggestion + "?\n  Type a goal of more than one word to start planning, or /marshal help."
			if err != nil || got != want {
				t.Errorf("marshal %s = %q, %v; want %q", tc.word, got, err, want)
			}
			if ws.marshal != nil || ws.marshalPanel() != nil {
				t.Fatal("typo started a Marshal run")
			}
		})
	}
}

func TestMarshalGoalsStillStartPlanning(t *testing.T) {
	for _, goal := range []string{"add a remove command", "refactor", "setings please"} {
		t.Run(goal, func(t *testing.T) {
			ws := NewWorkspace(nil, "proj", "sess-1")
			got, err := (&CommandHandler{ws: ws}).handleMarshal(context.Background(), strings.Fields(goal))
			if err != nil || got != "Marshal is preparing and drafting a plan for: "+goal {
				t.Fatalf("goal = %q, %v", got, err)
			}
			if ws.marshal == nil || ws.marshalPanel() == nil || ws.marshalPanel().RunID == "" {
				t.Fatal("goal did not start a Marshal run")
			}
			ws.marshalStop()
		})
	}
}

func TestBareMarshalShowsStatusAndUsage(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	h := &CommandHandler{ws: ws}
	for _, panel := range []*MarshalPanel{nil, {RunID: "RUN-test", Note: "waiting for approval"}} {
		ws.state.Marshal = panel
		got, err := h.handleMarshal(context.Background(), nil)
		want := marshalStatusText(panel) + "\n\n" + marshalUsage
		if err != nil || got != want {
			t.Fatalf("bare marshal = %q, %v; want %q", got, err, want)
		}
		if ws.marshal != nil {
			t.Fatal("bare marshal created a model session")
		}
	}
	if !strings.Contains(marshalUsage, "/marshal                         Show status and usage") {
		t.Fatal("usage still advertises a conversation")
	}
	if _, err := h.handleMarshal(context.Background(), []string{"chat"}); err == nil || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("explicit chat did not reach conversation: %v", err)
	}
}

func TestMarshalCompletionListsEverySubcommand(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	want := []string{"chat", "approve", "status", "close", "amend", "resume", "stop", "model", "settings", "help", "accept", "return", "use-plan", "approve-task"}
	_, matches := ws.completer.Suggest("/marshal ", len("/marshal "))
	if len(matches) == 0 || matches[0] != "chat" {
		t.Errorf("first suggestion = %v, want chat", matches)
	}
	got := ws.completer.ctx.Subcommands["/marshal"]
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("subcommands = %v, want %v", got, want)
	}
}

func TestMemoryConfigurationAvailableWithoutStore(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ws.workDir = t.TempDir()
	h := &CommandHandler{ws: ws}
	for _, args := range [][]string{{"inject"}, {"peers"}} {
		got, err := h.handleMemory(context.Background(), args, "/memory "+args[0])
		if err != nil || got == "Store unavailable" {
			t.Fatalf("memory %s = %q, %v", args[0], got, err)
		}
	}
}

func TestComposerHintsDescribeEnter(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	help := (&CommandHandler{ws: ws}).helpText()
	if strings.Contains(help, "never submits") || !strings.Contains(help, "finished command") {
		t.Fatalf("misleading Enter help: %s", help)
	}
	popup := strings.Join(renderCompletionPopup([]string{"chat", "settings"}, 0, nil, 160), "\n")
	if strings.Contains(popup, "Enter run") || !strings.Contains(popup, "Enter accept/run") {
		t.Fatalf("misleading popup hint: %s", popup)
	}
}

func TestMarshalActionsRejectExtraArguments(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	h := &CommandHandler{ws: ws}
	for _, sub := range []string{"help", "status", "approve", "close", "stop", "resume"} {
		got, err := h.handleMarshal(context.Background(), []string{sub, "unexpected"})
		if err == nil || err.Error() != "usage: /marshal "+sub {
			t.Errorf("marshal %s unexpected = %q, %v", sub, got, err)
		}
	}
	if ws.marshal != nil {
		t.Fatal("malformed action touched Marshal session")
	}
}

func TestMemoryInjectRejectsExtraArguments(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ws.workDir = t.TempDir()
	h := &CommandHandler{ws: ws}
	for _, args := range [][]string{{"off", "unexpected"}, {"clear", "unexpected"}, {"preview", "claude", "unexpected"}} {
		got, err := h.handleMemoryInject(context.Background(), args)
		if err != nil || !strings.HasPrefix(got, "Usage: /memory inject") {
			t.Errorf("inject %v = %q, %v", args, got, err)
		}
	}
	if got := loadInjectChannel(ws.workDir); got != injectAuto {
		t.Fatalf("malformed command changed channel to %s", got)
	}
}
