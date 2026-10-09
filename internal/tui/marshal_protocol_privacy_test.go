package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestMarshalLaunchHiddenProtocolMatrix(t *testing.T) {
	protocol, err := constitution.MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		for _, resumed := range []bool{false, true} {
			t.Run(provider+map[bool]string{false: "/fresh", true: "/resume"}[resumed], func(t *testing.T) {
				root := t.TempDir()
				if err := saveInjectChannel(root, injectOff); err != nil {
					t.Fatal(err)
				}
				var base []string
				if resumed {
					base = resumeArgsForProvider(provider, "bound-session")
				}
				args, env, dir, err := prepareMarshalLaunch(provider, root, base, protocol)
				if err != nil {
					t.Fatal(err)
				}
				defer dir.remove()
				opening := marshalKickoffArgs(provider)
				if strings.Join(args[len(args)-len(opening):], "\x00") != strings.Join(opening, "\x00") {
					t.Fatalf("wrong kickoff: %q", args)
				}
				if containsMarshalProtocol(args[len(args)-1]) {
					t.Fatal("protocol in visible opening")
				}
				var hidden string
				switch provider {
				case "codex":
					if args[0] != "-c" || !strings.HasPrefix(args[1], "developer_instructions=") {
						t.Fatal("missing developer instructions")
					}
					hidden = strings.TrimPrefix(args[1], "developer_instructions=")
				case "claude":
					if args[0] != "--append-system-prompt" {
						t.Fatal("missing system prompt")
					}
					hidden = args[1]
				default:
					data, err := os.ReadFile(dir.file())
					if err != nil {
						t.Fatal(err)
					}
					hidden = strings.TrimSpace(string(data))
					if provider == "opencode" && (len(env) != 1 || !strings.Contains(env[0], dir.file())) {
						t.Fatal("instructions not configured")
					}
					if provider == "antigravity" && (args[0] != "--add-dir" || args[1] != dir.path) {
						t.Fatal("workspace not configured")
					}
					for _, arg := range args {
						if containsMarshalProtocol(arg) {
							t.Fatal("file protocol leaked into argv")
						}
					}
				}
				if hidden != strings.TrimSpace(protocol) {
					t.Fatal("hidden channel lost or changed protocol")
				}
				if resumed && !strings.Contains(strings.Join(args, "\x00"), "bound-session") {
					t.Fatal("resume binding lost")
				}
			})
		}
	}
}

func TestMarshalLaunchRefusesMissingOrFailedHiddenDelivery(t *testing.T) {
	for _, tc := range []struct {
		provider           string
		badRoot, badConfig bool
	}{
		{provider: "unknown"}, {provider: "opencode", badRoot: true}, {provider: "antigravity", badRoot: true}, {provider: "opencode", badConfig: true},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			root := t.TempDir()
			if tc.badRoot {
				if err := os.WriteFile(filepath.Join(root, ".marshal"), []byte("blocked"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.badConfig {
				t.Setenv("OPENCODE_CONFIG_CONTENT", "invalid")
			}
			args, env, dir, err := prepareMarshalLaunch(tc.provider, root, nil, "MARSHAL PROTOCOL\nprivate")
			if err == nil || !strings.Contains(err.Error(), "Marshal not started:") || containsMarshalProtocol(err.Error()) {
				t.Fatalf("unsafe refusal: %v", err)
			}
			if len(args) != 0 || len(env) != 0 || dir != nil {
				t.Fatal("failed delivery returned launch material")
			}
		})
	}
}

func TestMarshalProtocolWithheldFromHistoricalSurfaces(t *testing.T) {
	protocol, err := constitution.MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{protocol, "[user] " + protocol, marshalProtocolLines[0]} {
		if got := RedactContent(text, nil); got != "[REDACTED]" {
			t.Fatal("protocol redaction failed")
		}
		if got := memoryExcerpt(model.MemoryRecordV2{Body: text}).Display(); containsMarshalProtocol(got) {
			t.Fatal("memory leaked protocol")
		}
		if got := (briefingLine{text: text}).String(); containsMarshalProtocol(got) {
			t.Fatal("briefing leaked protocol")
		}
		w := NewWorkspace(nil, "project", "session")
		w.RecordActivity(text)
		if containsMarshalProtocol(w.state.LastOutput) {
			t.Fatal("activity leaked protocol")
		}
		state := UIState{LastOutput: text, LastCommand: "/evidence"}
		rendered := strings.Join(outputSection(state, NewTheme(ThemeNoColor, false, false), 100), "\n")
		if containsMarshalProtocol(rendered) {
			t.Fatal("output leaked protocol")
		}
		w.state.Claims = []model.Claim{{ID: "C-private", NormalizedText: text, SupportingEvidence: []model.EvidenceRef{{EvidenceID: "EV-private"}}}}
		out, err := (&CommandHandler{ws: w}).handleEvidence(context.Background(), "EV-private")
		if err != nil || containsMarshalProtocol(out) || !strings.Contains(out, "[REDACTED]") {
			t.Fatal("evidence leaked protocol")
		}
		stream, err := openStream(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stream.append("codex", "session", importer.Message{Role: "user", Content: text}); err != nil {
			t.Fatal(err)
		}
		entries, err := stream.since(-1)
		if err != nil || len(entries) != 1 || entries[0].Text != "[REDACTED]" {
			t.Fatal("peer stream leaked protocol")
		}
	}
}

func TestMarshalRestartRebuildsHiddenDelivery(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		for _, saved := range []bool{false, true} {
			t.Run(provider+map[bool]string{false: "/fresh", true: "/saved_intake"}[saved], func(t *testing.T) {
				_, log := setupFakeTmux(t)
				root := t.TempDir()
				wantOpening := marshalKickoff
				if saved {
					wantOpening = marshalKickoffContinue
					if err := os.MkdirAll(filepath.Join(root, ".marshal"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(root, ".marshal", "marshal-intake.json"), []byte(`{"language":"Uzbek","earlier_work":"no"}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
				w := NewWorkspace(nil, "project", "session")
				w.tmuxSession = "test-session"
				a := &activeTmuxAgent{provider: provider, binary: provider, paneID: "%chat", window: "chat", sessionID: "bound-session", args: []string{"MARSHAL PROTOCOL\nlegacy visible kickoff"}}
				w.restartMarshalChat(context.Background(), a, root)
				if len(a.args) == 0 || a.args[len(a.args)-1] != wantOpening {
					t.Fatal("restart reused legacy visible kickoff")
				}
				if !strings.Contains(strings.Join(a.args, "\n"), "bound-session") {
					t.Fatal("restart lost bound conversation")
				}
				var hidden string
				switch provider {
				case "codex":
					hidden = a.args[1]
					if a.args[0] != "-c" || !strings.Contains(a.args[1], "MARSHAL PROTOCOL") {
						t.Fatal("restart lost developer instructions")
					}
				case "claude":
					hidden = a.args[1]
					if a.args[0] != "--append-system-prompt" || !strings.Contains(a.args[1], "MARSHAL PROTOCOL") {
						t.Fatal("restart lost system prompt")
					}
				default:
					defer a.briefingDir.remove()
					data, err := os.ReadFile(a.briefingDir.file())
					if err != nil || !strings.Contains(string(data), "MARSHAL PROTOCOL") {
						t.Fatal("restart lost instruction file")
					}
					hidden = string(data)
					data, err = os.ReadFile(log)
					if err != nil {
						t.Fatal(err)
					}
					if provider == "opencode" && (!strings.Contains(string(data), "OPENCODE_CONFIG_CONTENT=") || !strings.Contains(string(data), a.briefingDir.file())) {
						t.Fatal("respawn lost instructions environment")
					}
				}
				if saved && (strings.Count(hidden, "\nPROJECT INTAKE (saved preference data): ") != 1 || !strings.Contains(hidden, `{"language":"Uzbek","earlier_work":"no"}`)) {
					t.Fatal("restart lost or duplicated saved intake")
				}
			})
		}
	}
}

func TestMarshalRestartRefusesFailedHiddenDelivery(t *testing.T) {
	_, log := setupFakeTmux(t)
	t.Setenv("OPENCODE_CONFIG_CONTENT", "invalid")
	w := NewWorkspace(nil, "project", "session")
	w.tmuxSession = "test-session"
	a := &activeTmuxAgent{provider: "opencode", binary: "opencode", paneID: "%chat", window: "chat"}
	w.restartMarshalChat(context.Background(), a, t.TempDir())
	data, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "respawn-window") || strings.Contains(string(data), "respawn-pane") || strings.Contains(string(data), "new-window") {
		t.Fatal("restart launched after failed hidden delivery")
	}
	if !strings.Contains(w.state.LastOutput, "Marshal not started:") {
		t.Fatal("no operator refusal")
	}
}

func TestMarshalHiddenInstructionsSurviveLastProviderOverride(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			flag, key := "--append-system-prompt", ""
			if provider == "codex" {
				flag, key = "-c", "developer_instructions="
			}
			base := []string{flag, key + "first", flag, key + "last"}
			args, _, dir, err := prepareMarshalLaunch(provider, t.TempDir(), base, "MARSHAL PROTOCOL\nmandatory")
			defer dir.remove()
			if err != nil {
				t.Fatal(err)
			}
			if args[1] != base[1] || args[3] != base[3]+"\n\nMARSHAL PROTOCOL\nmandatory" {
				t.Fatal("protocol dropped behind last provider override")
			}
		})
	}
}

func TestMarshalHiddenDeliveryRejectsNullOpenCodeConfig(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "null")
	args, env, dir, err := prepareMarshalLaunch("opencode", t.TempDir(), nil, "MARSHAL PROTOCOL")
	if err == nil || len(args) != 0 || len(env) != 0 || dir != nil || !strings.Contains(err.Error(), "Marshal not started:") {
		t.Fatal("null config did not refuse hidden delivery")
	}
}

func TestMarshalProtocolWithheldFromStatus(t *testing.T) {
	_, log := setupFakeTmux(t)
	w := NewWorkspace(nil, "project", "session")
	w.tmuxPath = "tmux"
	w.tmuxSession = "test-session"
	w.tmuxMarshalWinID = "@0"
	w.tmuxAlerts = map[string]string{"private": "MARSHAL PROTOCOL\nprivate instructions"}
	w.updateTmuxStatusLine(context.Background())
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if containsMarshalProtocol(string(data)) || !strings.Contains(string(data), "[REDACTED]") {
		t.Fatal("status line leaked protocol")
	}
	root := t.TempDir()
	if err := writeLiveStatus(root, "codex", liveStatus{Error: "MARSHAL PROTOCOL\nprivate instructions"}); err != nil {
		t.Fatal(err)
	}
	status, err := readLiveStatus(root, "codex")
	if err != nil || status.Error != "[REDACTED]" {
		t.Fatal("capture status leaked protocol")
	}
}

func TestMarshalProtocolCannotUseVisibleOrDisabledChannels(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity", "unknown"} {
		for _, channel := range []injectChannel{injectPrompt, injectProjectDoc, injectOff} {
			t.Run(provider+"/"+string(channel), func(t *testing.T) {
				root := t.TempDir()
				original := []string{"operator-argument"}
				args, _, err := applyBriefing(provider, root, original, "MARSHAL PROTOCOL\nprivate", channel)
				if err == nil || strings.Join(args, "\x00") != strings.Join(original, "\x00") {
					t.Fatal("protocol fell back to visible or disabled channel")
				}
				if _, err := os.Stat(filepath.Join(root, projectDocName(provider))); !os.IsNotExist(err) {
					t.Fatal("protocol written into project documents")
				}
			})
		}
	}
}

func TestMarshalLaunchScrubsRetainedProtocolInboxes(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, ".marshal", "inbox")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []string{"codex", "claude", "opencode", "antigravity", "marshal"} {
		if err := os.WriteFile(inboxPath(root, reader), []byte("old history\nMARSHAL PROTOCOL\nprivate instructions"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ordinary := filepath.Join(base, "ordinary.md")
	if err := os.WriteFile(ordinary, []byte("ordinary history"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, dir, err := prepareMarshalLaunch("codex", root, nil, "MARSHAL PROTOCOL\nmandatory")
	defer dir.remove()
	if err != nil {
		t.Fatal(err)
	}
	for _, reader := range []string{"codex", "claude", "opencode", "antigravity", "marshal"} {
		data, err := os.ReadFile(inboxPath(root, reader))
		if err != nil || containsMarshalProtocol(string(data)) || !strings.Contains(string(data), "[REDACTED]") {
			t.Fatal("retained inbox leaked protocol")
		}
	}
	data, err := os.ReadFile(ordinary)
	if err != nil || string(data) != "ordinary history" {
		t.Fatal("ordinary inbox changed")
	}
}

func TestPeerInboxOpeningScrubsOldProtocol(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(inboxPath(root, "codex")), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inboxPath(root, "codex"), []byte("MARSHAL PROTOCOL\nprivate instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openInboxView(root, "codex", true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(inboxPath(root, "codex"))
	if err != nil || containsMarshalProtocol(string(data)) || !strings.Contains(string(data), "session opened") {
		t.Fatal("peer opening retained old protocol")
	}
}

// TestMarshalProtocolNeverVisibleInProviderChatTurn enforces that the Marshal's
// protocol instructions and saved project intake are never exposed as a visible
// turn in the provider chat (e.g. U5-reopened finding). Fresh launches must use
// a neutral opening turn ("Start."), and launches with saved intake must use
// "Continue." with the intake delivered exclusively through the hidden instruction channel.
func TestMarshalProtocolNeverVisibleInProviderChatTurn(t *testing.T) {
	protocol, err := marshalRoleBriefing([]string{"codex"}, marshal.DefaultSettings(), marshal.Standard)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		for _, mode := range []string{"fresh", "saved_intake", "resume_saved_intake"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				root := t.TempDir()
				var base []string
				if mode == "resume_saved_intake" {
					base = resumeArgsForProvider(provider, "session-123")
				}
				if mode != "fresh" {
					if err := os.MkdirAll(filepath.Join(root, ".marshal"), 0700); err != nil {
						t.Fatal(err)
					}
					data := `{"language":"Uzbek","earlier_work":"no"}`
					if err := os.WriteFile(filepath.Join(root, ".marshal", "marshal-intake.json"), []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
				args, _, dir, err := prepareMarshalLaunch(provider, root, base, protocol)
				if dir != nil {
					defer dir.remove()
				}
				if err != nil {
					t.Fatal(err)
				}
				visibleOpening := args[len(args)-1]
				wantOpening := "Start."
				if mode != "fresh" {
					wantOpening = "Continue."
				}
				if visibleOpening != wantOpening {
					t.Fatalf("visible opening = %q, want %q", visibleOpening, wantOpening)
				}
				if strings.Contains(visibleOpening, "{") || strings.Contains(visibleOpening, "Uzbek") ||
					strings.Contains(visibleOpening, "Begin at step") || strings.Contains(visibleOpening, "protocol") ||
					strings.Contains(visibleOpening, "intake") {
					t.Fatalf("protocol or JSON leaked into visible opening: %q", visibleOpening)
				}
				// Verify hidden instruction channel delivery
				var hidden string
				switch provider {
				case "codex":
					if args[0] != "-c" || !strings.HasPrefix(args[1], "developer_instructions=") {
						t.Fatal("missing developer instructions")
					}
					hidden = strings.TrimPrefix(args[1], "developer_instructions=")
				case "claude":
					if args[0] != "--append-system-prompt" {
						t.Fatal("missing system prompt")
					}
					hidden = args[1]
				default:
					content, err := os.ReadFile(dir.file())
					if err != nil {
						t.Fatal(err)
					}
					hidden = string(content)
				}
				if !strings.Contains(hidden, `"Start."`) || !strings.Contains(hidden, `"Continue."`) {
					t.Fatal("hidden protocol does not define neutral start cues")
				}
				if !strings.Contains(hidden, "MARSHAL PROTOCOL") {
					t.Fatal("protocol missing from hidden instructions")
				}
				if mode != "fresh" {
					if strings.Count(hidden, "\nPROJECT INTAKE (saved preference data): ") != 1 || !strings.Contains(hidden, `{"language":"Uzbek","earlier_work":"no"}`) {
						t.Fatalf("saved intake missing from hidden channel: %s", hidden)
					}
				}
			})
		}
	}
}
