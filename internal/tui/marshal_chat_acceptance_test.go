package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/memory/importer"
)

func TestMarshalRewrittenDraftRefusesApprovalAndCanDiscard(t *testing.T) {
	w := recoveryChatWorkspace(t)
	if _, err := w.marshalChat(t.Context()); err != nil {
		t.Fatal(err)
	}
	stopRecoveryDraftWatcher(w)
	writeRecoveryChatDraft(t, w.providerRoot())
	if _, err := w.marshalChat(t.Context()); err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	m.mu.Lock()
	service, runID := m.service, m.runID
	m.mu.Unlock()
	before, err := service.Snapshot(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := service.PlanApprovalSnapshot(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	writeRecoveryChatDraft(t, w.providerRoot())
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "rewrite", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"approve"}`}}})
	if batch := w.permissions.queue.Take(false); len(batch) != 0 {
		t.Errorf("rewritten draft queued approval of stored plan: %+v", batch)
	}
	for _, snapshot := range []*app.PlanApprovalSnapshot{nil, &reviewed} {
		if _, err := w.marshalApproveReviewed(t.Context(), snapshot); err == nil || !strings.Contains(err.Error(), "/marshal draft discard") {
			t.Fatalf("approval refusal: %v", err)
		}
	}
	after, err := service.Snapshot(t.Context(), runID)
	if err != nil || after.State != marshal.Drafting || after.PlanVersion != before.PlanVersion {
		t.Fatalf("stored plan changed: %+v %v", after, err)
	}
	if _, err := w.ExecuteCommand(t.Context(), "/marshal draft discard"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{marshalDraftRelativePath, marshalDraftRelativePath + ".ready"} {
		if _, err := os.Lstat(filepath.Join(w.providerRoot(), path)); !os.IsNotExist(err) {
			t.Fatalf("leftover %s: %v", path, err)
		}
	}
	if _, err := w.marshalChat(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalClosedRunLeftoverOffersWorkingNextStep(t *testing.T) {
	w := recoveryChatWorkspace(t)
	root := w.providerRoot()
	run := marshal.Run{PlanID: "plan", PlanVersion: 1, State: marshal.Closed, Settings: marshal.DefaultSettings()}
	if _, err := w.runtime.Marshal().Store.SetMarshalRun(t.Context(), w.projectID, "closed", run, 0); err != nil {
		t.Fatal(err)
	}
	if err := saveChatBinding(root, chatBinding{Provider: "codex", RunID: "closed"}); err != nil {
		t.Fatal(err)
	}
	writeRecoveryChatDraft(t, root)
	if message := marshalChatClosedMessage(root); !strings.Contains(message, "/marshal draft discard") || strings.Contains(message, "Use /marshal chat to reopen it") {
		t.Fatalf("contradictory closure: %s", message)
	}
	if _, err := w.marshalChat(t.Context()); err == nil || !strings.Contains(err.Error(), "/marshal draft discard") {
		t.Fatalf("reopen refusal: %v", err)
	}
	if _, err := w.ExecuteCommand(t.Context(), "/marshal draft discard"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.marshalChat(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalNativeLaunchDeliversIntakeCueAllProviders(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		for _, saved := range []bool{false, true} {
			t.Run(provider+map[bool]string{false: "/fresh", true: "/saved"}[saved], func(t *testing.T) {
				w := recoveryChatWorkspace(t)
				bin := t.TempDir()
				for _, name := range []string{"codex", "claude", "opencode", "agy"} {
					if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho '0.159.2'\n"), 0755); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
				want := "Start."
				if saved {
					if err := w.saveMarshalIntake(marshalIntake{Language: "Uzbek", EarlierWork: "no"}); err != nil {
						t.Fatal(err)
					}
					want = "Continue."
				}
				protocol, err := marshalRoleBriefing([]string{"codex"}, marshal.DefaultSettings(), marshal.Standard)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.runNativeAgent(t.Context(), provider, nil, protocol); err != nil {
					t.Fatal(err)
				}
				w.tmuxMu.Lock()
				a := copyAgentLocked(w.tmuxActiveWins["marshal-chat"])
				w.tmuxMu.Unlock()
				if a == nil || len(a.args) == 0 {
					t.Fatal("no Marshal launch")
				}
				visible := a.args[len(a.args)-1]
				if visible != want || containsMarshalProtocol(visible) || strings.ContainsAny(visible, "{}") {
					t.Fatalf("visible turn: %q", visible)
				}
				hidden := strings.Join(a.args[:len(a.args)-1], "\n")
				if provider == "opencode" || provider == "antigravity" {
					if a.briefingDir == nil {
						t.Fatal("no hidden briefing directory")
					}
					data, err := os.ReadFile(a.briefingDir.file())
					if err != nil {
						t.Fatal(err)
					}
					hidden = string(data)
				}
				if !strings.Contains(hidden, strings.TrimSpace(protocol)) || !strings.Contains(hidden, `"Start."`) || !strings.Contains(hidden, `"Continue."`) {
					t.Fatal("hidden protocol lost in native launch")
				}
				if saved && !strings.Contains(hidden, `"language":"Uzbek"`) {
					t.Fatal("saved intake not delivered")
				}
			})
		}
	}
}
