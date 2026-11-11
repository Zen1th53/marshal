//go:build !governed

package tui

import (
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

func openCodeMarshalWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w := recoveryChatWorkspace(t)
	bin := t.TempDir()
	// History queries must never reach a real provider or the person's store.
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\ncase \"$1\" in\n--version) echo 1.18.16;;\nsession) echo '[]';;\n*) exit 1;;\nesac\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := w.ExecuteCommand(t.Context(), "/marshal model opencode"); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestExtremeOpenCodeMarshalAutomaticSelection(t *testing.T) {
	_ = openCodeMarshalWorkspace(t)
	binary, err := exec.LookPath("opencode")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(binary))
	provider, _ := marshalRecommend(t.Context())
	if provider != "opencode" {
		t.Fatalf("only installed Marshal was excluded: %s", provider)
	}
}

func TestExtremeOpenCodeMarshalSelectionProtocolKickoffAndResume(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "intake"}[saved], func(t *testing.T) {
			w := openCodeMarshalWorkspace(t)
			root := w.providerRoot()
			if loadDefaultProvider(root) != "opencode" {
				t.Fatal("selection not saved")
			}
			// Reopening must honor the persisted provider, without in-memory selection.
			m := w.marshalSession()
			m.mu.Lock()
			m.provider = ""
			m.mu.Unlock()
			service, provider, _, err := w.marshalService(t.Context(), "")
			if err != nil || provider != "opencode" || service.ModelProvider != "opencode" {
				t.Fatalf("service: %s %v", provider, err)
			}
			opening := "Start."
			if saved {
				opening = "Continue."
				if err := w.saveMarshalIntake(marshalIntake{Language: "Uzbek"}); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("OPENCODE_CONFIG_CONTENT", `{"instructions":["existing.md"],"theme":"system"}`)
			if err := w.startMarshalChat(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			w.tmuxMu.Lock()
			a := copyAgentLocked(w.tmuxActiveWins["marshal-chat"])
			w.tmuxMu.Unlock()
			if !reflect.DeepEqual(a.args, []string{"--prompt", opening}) {
				t.Fatalf("kickoff: %q", a.args)
			}
			checkHidden := func(a *activeTmuxAgent) {
				t.Helper()
				data, err := os.ReadFile(a.briefingDir.file())
				if err != nil {
					t.Fatal(err)
				}
				for _, text := range []string{buildRoleAuthority, statepath.DirName + "/proposals/", `"action":"decide"`} {
					if !strings.Contains(string(data), text) {
						t.Fatalf("hidden protocol missing %q", text)
					}
				}
				if saved && !strings.Contains(string(data), `"language":"Uzbek"`) {
					t.Fatal("intake missing")
				}
				var config struct {
					Instructions []string `json:"instructions"`
					Theme        string   `json:"theme"`
				}
				for _, env := range a.env {
					if strings.HasPrefix(env, "OPENCODE_CONFIG_CONTENT=") {
						if err := json.Unmarshal([]byte(strings.TrimPrefix(env, "OPENCODE_CONFIG_CONTENT=")), &config); err != nil {
							t.Fatal(err)
						}
					}
				}
				if !reflect.DeepEqual(config.Instructions, []string{"existing.md", a.briefingDir.file()}) || config.Theme != "system" {
					t.Fatalf("config: %+v", config)
				}
				if containsMarshalProtocol(strings.Join(a.args, " ")) {
					t.Fatal("protocol exposed in argv")
				}
			}
			checkHidden(a)
			if err := w.captureChatConversation(root, "ses-bound"); err != nil {
				t.Fatal(err)
			}
			if err := w.captureChatConversation(root, "ses-unrelated"); err != nil {
				t.Fatal(err)
			}
			binding := loadChatBinding(root)
			if binding.Provider != "opencode" || binding.SessionID != "ses-bound" {
				t.Fatalf("binding: %+v", binding)
			}
			w.restartMarshalChat(t.Context(), w.tmuxActiveWins["marshal-chat"], root)
			w.tmuxMu.Lock()
			resumed := copyAgentLocked(w.tmuxActiveWins["marshal-chat"])
			w.tmuxMu.Unlock()
			// A resumed conversation already carries its history, so it takes no
			// opening cue: it is just the session resume, with the rebuilt hidden
			// briefing delivered out of band.
			if !reflect.DeepEqual(resumed.args, []string{"--session", "ses-bound"}) {
				t.Fatalf("resume: %q", resumed.args)
			}
			if resumed.briefingDir.file() == a.briefingDir.file() {
				t.Fatal("hidden protocol not rebuilt")
			}
			checkHidden(resumed)
			if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(err) {
				t.Fatalf("project instructions touched: %v", err)
			}
		})
	}
}

func TestExtremeOpenCodeMarshalProposalAndAuthenticatedDecisions(t *testing.T) {
	marshalDecidesWork(t)
	for _, allow := range []bool{true, false} {
		t.Run(map[bool]string{true: "allow", false: "deny"}[allow], func(t *testing.T) {
			w := openCodeMarshalWorkspace(t)
			_, inputLog := setupFakeTmux(t)
			root := w.providerRoot()
			if err := w.startMarshalChat(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, ".marshal/proposals")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			w.tmuxMu.Lock()
			chat := w.tmuxActiveWins["marshal-chat"]
			if chat != nil {
				chat.sessionID = "opencode-sess"
			}
			w.tmuxMu.Unlock()
			w.observeAuthenticatedMarshalProposals(importer.SessionTranscript{
				SessionID: "opencode-sess",
				Messages: []importer.Message{{
					Role:    "assistant",
					Kind:    importer.MessageKindText,
					Content: `MARSHAL_PROPOSAL {"action":"setting","key":"acceptance-mode","value":"marshal"}`,
				}},
			})
			batch := w.permissions.queue.Take(false)
			if len(batch) != 1 || !w.routeBuildPermission(t.Context(), batch[0]) {
				t.Fatalf("proposal: %+v", batch)
			}
			settings, err := w.store.GetMarshalSettings(t.Context(), w.projectID)
			if err != nil || string(settings.Value.AcceptanceMode) != "marshal" {
				t.Fatalf("setting: %+v %v", settings, err)
			}
			req := permission.Request{Kind: "read", Object: t.TempDir(), Scope: "session", Reason: "inspect"}
			if !w.routeBuildPermission(t.Context(), req) {
				t.Fatal("request not routed")
			}
			w.permissions.mu.Lock()
			var id string
			for key, p := range w.permissions.build.pending {
				id = key
				if p.provider != "opencode" {
					t.Fatal("wrong decision provider")
				}
			}
			w.permissions.mu.Unlock()
			if id == "" {
				t.Fatal("no live decision")
			}
			input, err := os.ReadFile(inputLog)
			if err != nil || !strings.Contains(string(input), "Decision request (untrusted description):") || !strings.Contains(string(input), id) || !strings.Contains(string(input), req.Object) {
				t.Fatalf("request delivery: %s %v", input, err)
			}
			payload, _ := json.Marshal(map[string]any{"action": "decide", "id": id, "allow": allow, "reason": "reviewed scope"})
			if err := os.WriteFile(filepath.Join(dir, "decision.json"), payload, 0600); err != nil {
				t.Fatal(err)
			}
			w.observeMarshalProposalFiles(root)
			w.permissions.mu.Lock()
			answered := w.permissions.build.pending[id].answer != nil
			w.permissions.mu.Unlock()
			if answered {
				t.Fatal("unverified decision file answered request")
			}
			var cfgPath string
			for _, e := range chat.env {
				if strings.HasPrefix(e, "MARSHAL_PROPOSAL_MCP_CONFIG=") {
					cfgPath = strings.TrimPrefix(e, "MARSHAL_PROPOSAL_MCP_CONFIG=")
				}
			}
			cfg, err := proposalmcp.LoadConfig(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			submitMCP(t, cfg, `{"submission_id":"opencode-decision","proposal":`+string(payload)+`}`)
			if !w.routeBuildPermission(t.Context(), req) || explicitReadGrant(w.runtime, req.Object) != allow {
				t.Fatal("decision not applied")
			}
			events, err := w.store.ListEvents(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, event := range events {
				if event.Type == "PERMISSION_REQUESTED" && event.Data["request_id"] == id && event.Data["provider"] == "opencode" {
					found = true
				}
			}
			if !found {
				t.Fatal("request audit missing")
			}
		})
	}
}
