package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Codex has no system-prompt flag. A channel it cannot honour must fall back to
// one it can, and say so, rather than silently delivering nothing.
func TestResolveInjectChannelFallsBackPerProvider(t *testing.T) {
	for _, tc := range []struct {
		name       string
		provider   string
		configured injectChannel
		want       injectChannel
		wantNote   bool
	}{
		{"claude auto uses system prompt", "claude", injectAuto, injectSystemPrompt, false},
		{"codex auto uses developer instructions", "codex", injectAuto, injectDeveloperInstructions, false},
		{"opencode auto uses MARSHAL's directory", "opencode", injectAuto, injectMarshalDir, false},
		{"antigravity auto uses MARSHAL's directory", "antigravity", injectAuto, injectMarshalDir, false},
		{"codex cannot take a system prompt", "codex", injectSystemPrompt, injectDeveloperInstructions, true},
		{"claude takes a system prompt", "claude", injectSystemPrompt, injectSystemPrompt, false},
		{"opencode cannot take a system prompt", "opencode", injectSystemPrompt, injectMarshalDir, true},
		{"project doc stays available when chosen", "codex", injectProjectDoc, injectProjectDoc, false},
		{"prompt channel is universal", "codex", injectPrompt, injectPrompt, false},
		{"off stays off", "claude", injectOff, injectOff, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, note := resolveInjectChannel(tc.provider, tc.configured)
			if got != tc.want {
				t.Errorf("channel = %q, want %q", got, tc.want)
			}
			if (note != "") != tc.wantNote {
				t.Errorf("note = %q, wantNote = %v", note, tc.wantNote)
			}
		})
	}
}

func TestInjectChannelRoundTripsAndRejectsUnknown(t *testing.T) {
	root := t.TempDir()
	if got := loadInjectChannel(root); got != injectAuto {
		t.Errorf("unset channel = %q, want auto", got)
	}
	if err := saveInjectChannel(root, injectPrompt); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := loadInjectChannel(root); got != injectPrompt {
		t.Errorf("reloaded channel = %q, want prompt", got)
	}
	if _, err := parseInjectChannel("telepathy"); err == nil {
		t.Error("unknown channel was accepted")
	}
}

func TestApplyBriefingSystemPromptChannel(t *testing.T) {
	root := t.TempDir()
	args, note, err := applyBriefing("claude", root, []string{"--continue"}, "prior work", injectSystemPrompt)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(args) != 3 || args[0] != "--append-system-prompt" || args[1] != "prior work" || args[2] != "--continue" {
		t.Fatalf("argv = %q", args)
	}
	if note == "" {
		t.Error("operator was not told the briefing was injected")
	}

	// The operator's own system prompt is kept, and the briefing joins it
	// rather than replacing it or being dropped.
	operator := []string{"--append-system-prompt", "mine"}
	args, _, err = applyBriefing("claude", root, operator, "prior work", injectSystemPrompt)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(args) != 2 || args[1] != "mine\n\nprior work" {
		t.Errorf("briefing did not join the operator's system prompt: %q", args)
	}
	args, _, err = applyBriefing("claude", root, []string{"--append-system-prompt=mine"}, "prior work", injectSystemPrompt)
	if err != nil || len(args) != 1 || args[0] != "--append-system-prompt=mine\n\nprior work" {
		t.Errorf("briefing did not join the = form: %q %v", args, err)
	}
}

// The Marshal protocol reaches Claude even when the cross-agent memory was
// delivered first on the same system prompt; skipping it left Claude with the
// memory and no protocol.
func TestClaudeMarshalProtocolFollowsTheMemory(t *testing.T) {
	root := t.TempDir()
	args, _, err := applyBriefing("claude", root, nil, "What other agents did.", injectSystemPrompt)
	if err != nil {
		t.Fatal(err)
	}
	args, _, err = applyBriefing("claude", root, args, "MARSHAL PROTOCOL", injectSystemPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || !strings.Contains(args[1], "What other agents did.") || !strings.Contains(args[1], "MARSHAL PROTOCOL") {
		t.Fatalf("args = %q", args)
	}
}

func TestApplyBriefingPromptChannelAppends(t *testing.T) {
	args, _, err := applyBriefing("codex", t.TempDir(), nil, "prior work", injectPrompt)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(args) != 1 || !strings.Contains(args[0], "prior work") {
		t.Fatalf("argv = %q", args)
	}
}

func TestApplyBriefingUsesOpenCodePromptFlag(t *testing.T) {
	args, _, err := applyBriefing("opencode", t.TempDir(), []string{"--continue"}, "prior work", injectPrompt)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(args) != 3 || args[0] != "--prompt" || !strings.Contains(args[1], "prior work") || args[2] != "--continue" {
		t.Fatalf("argv = %q", args)
	}
}

// An operator prompt already occupies the positional slot, so a second one
// would be handed to the CLI as a stray argument.
func TestHasOperatorPrompt(t *testing.T) {
	if !hasOperatorPrompt([]string{"--", "do the thing"}) {
		t.Error("prompt after -- not detected")
	}
	if hasOperatorPrompt([]string{"--continue"}) {
		t.Error("flag-only argv reported as carrying a prompt")
	}
	if !hasOperatorPrompt([]string{"--prompt", "do the thing"}) {
		t.Error("OpenCode --prompt was not detected")
	}
}

// The project document belongs to the operator. MARSHAL owns exactly its marked
// block and must leave everything else byte-identical across repeated writes.
func TestProjectDocBlockIsReplacedNotAppended(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	original := "# Project instructions\n\nAlways run the conformance suite.\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := writeProjectDocBlock(path, "first briefing"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := writeProjectDocBlock(path, "second briefing"); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, original) {
		t.Errorf("operator's own content was modified:\n%s", content)
	}
	if strings.Count(content, projectDocStartMarker) != 1 {
		t.Errorf("block was appended rather than replaced:\n%s", content)
	}
	if strings.Contains(content, "first briefing") {
		t.Errorf("stale briefing survived the rewrite:\n%s", content)
	}
	if !strings.Contains(content, "second briefing") {
		t.Errorf("current briefing missing:\n%s", content)
	}

	cleared, err := clearProjectDocBlock(root)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared != 1 {
		t.Errorf("cleared %d document(s), want 1", cleared)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after clear: %v", err)
	}
	if strings.Contains(string(data), projectDocStartMarker) {
		t.Errorf("block survived clear:\n%s", data)
	}
	if !strings.Contains(string(data), "Always run the conformance suite.") {
		t.Errorf("clear removed the operator's own content:\n%s", data)
	}
}

// A half-deleted marker means the file was hand-edited. Writing another block
// would compound the damage, so the write refuses instead.
func TestProjectDocBlockRefusesUnpairedMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(path, []byte("# Doc\n\n"+projectDocStartMarker+"\nleftover\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := writeProjectDocBlock(path, "briefing"); err == nil {
		t.Fatal("unpaired marker was accepted")
	}
}

// The briefing is created for a file that does not exist yet on most projects.
func TestProjectDocBlockCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := writeProjectDocBlock(path, "briefing"); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "briefing") {
		t.Errorf("briefing missing from new document:\n%s", data)
	}
}

// Nothing is delivered when there is nothing to say, and off means off.
func TestApplyBriefingNoOpCases(t *testing.T) {
	root := t.TempDir()
	base := []string{"--continue"}
	for _, tc := range []struct {
		name     string
		briefing string
		channel  injectChannel
	}{
		{"empty briefing", "   ", injectSystemPrompt},
		{"channel off", "prior work", injectOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, note, err := applyBriefing("claude", root, base, tc.briefing, tc.channel)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if len(args) != len(base) || note != "" {
				t.Errorf("argv = %q, note = %q; expected no change", args, note)
			}
		})
	}
}

// The briefing is delivered as a system prompt, so its header must frame
// another agent's output as data rather than as instructions to follow.
func TestBriefingHeaderMarksContentUntrusted(t *testing.T) {
	for _, want := range []string{"untrusted DATA", "not as instructions", "not verified facts"} {
		if !strings.Contains(briefingHeader, want) {
			t.Errorf("briefing header missing %q:\n%s", want, briefingHeader)
		}
	}
}

// Without a store there is nothing to compile, and that is not an error.
func TestBriefingRequiresStore(t *testing.T) {
	briefing, err := (&Workspace{}).crossAgentBriefing(t.Context(), "claude")
	if err != nil {
		t.Fatalf("briefing: %v", err)
	}
	if briefing != "" {
		t.Fatalf("a workspace with no store must produce no briefing, got %q", briefing)
	}
}

// Mandatory instructions must never fall back to a visible prompt.
func TestMarshalPromptDeliveryRefused(t *testing.T) {
	args, _, err := applyBriefing("codex", t.TempDir(), nil, "MARSHAL PROTOCOL\n\n1. Introduce yourself.", injectPrompt)
	if err == nil || len(args) != 0 {
		t.Fatalf("visible protocol delivery accepted: args=%v err=%v", args, err)
	}
	args, _, err = applyBriefing("codex", t.TempDir(), nil, "What other agents did.", injectPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if last := args[len(args)-1]; !strings.HasSuffix(last, "Acknowledge in one line, then wait for the operator.") {
		t.Fatalf("cross-agent prompt lost its acknowledgement: %q", last)
	}
}

// Codex receives briefings as developer instructions, which it follows
// without printing as a conversation turn. Codex keeps only the last
// developer_instructions override, so a second briefing joins the first.
func TestCodexBriefingsJoinInOneDeveloperInstruction(t *testing.T) {
	memory := "What other agents did."
	protocol := "MARSHAL PROTOCOL\n\n1. Introduce yourself."
	args, _, err := applyBriefing("codex", t.TempDir(), nil, memory, injectDeveloperInstructions)
	if err != nil {
		t.Fatal(err)
	}
	args, _, err = applyBriefing("codex", t.TempDir(), args, protocol, injectDeveloperInstructions)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-c", "developer_instructions=" + memory + "\n\n" + protocol}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %q, want %q", args, want)
	}
}

// Each CLI gets its opening turn in the form it accepts.
func TestMarshalKickoffArgsPerProvider(t *testing.T) {
	for provider, want := range map[string][]string{
		"codex":       {marshalKickoff},
		"claude":      {marshalKickoff},
		"opencode":    {"--prompt", marshalKickoff},
		"antigravity": {"--prompt-interactive", marshalKickoff},
	} {
		if got := marshalKickoffArgs(provider); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%s kickoff = %q, want %q", provider, got, want)
		}
	}
}

func TestMarshalSavedIntakeOpeningAllProviders(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		for _, earlier := range []string{"", "no", "yes"} {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".marshal"), 0700); err != nil {
				t.Fatal(err)
			}
			data := `{"language":"Uzbek","earlier_work":"` + earlier + `"}`
			if err := os.WriteFile(filepath.Join(root, ".marshal", "marshal-intake.json"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			args, _, dir, err := prepareMarshalLaunch(provider, root, nil, "MARSHAL PROTOCOL")
			if dir != nil {
				defer dir.remove()
			}
			if err != nil {
				t.Fatal(err)
			}
			opening := args[len(args)-1]
			if opening != marshalKickoffContinue {
				t.Fatalf("%s opening = %q, want %q", provider, opening, marshalKickoffContinue)
			}
			if strings.Contains(opening, "Uzbek") || strings.Contains(opening, "{") || strings.Contains(opening, "MARSHAL PROTOCOL") || strings.Contains(opening, "Do not repeat") {
				t.Fatalf("%s visible opening leaked intake: %s", provider, opening)
			}
			var hidden string
			switch provider {
			case "codex":
				if len(args) < 2 || args[0] != "-c" || !strings.HasPrefix(args[1], "developer_instructions=") {
					t.Fatalf("%s missing developer instructions: %q", provider, args)
				}
				hidden = strings.TrimPrefix(args[1], "developer_instructions=")
			case "claude":
				if len(args) < 2 || args[0] != "--append-system-prompt" {
					t.Fatalf("%s missing system prompt: %q", provider, args)
				}
				hidden = args[1]
			default:
				if dir == nil {
					t.Fatalf("%s missing briefing directory", provider)
				}
				content, err := os.ReadFile(dir.file())
				if err != nil {
					t.Fatalf("%s failed to read briefing file: %v", provider, err)
				}
				hidden = string(content)
			}
			if !strings.Contains(hidden, "MARSHAL PROTOCOL") {
				t.Fatalf("%s hidden instructions lost protocol: %s", provider, hidden)
			}
			if !strings.Contains(hidden, "PROJECT INTAKE") || !strings.Contains(hidden, "Uzbek") || !strings.Contains(hidden, "Do not repeat") {
				t.Fatalf("%s hidden instructions lost intake: %s", provider, hidden)
			}
			if earlier != "" && !strings.Contains(hidden, `"earlier_work":"`+earlier+`"`) {
				t.Fatalf("%s hidden instructions lost earlier_work: %s", provider, hidden)
			}
		}
	}
}
