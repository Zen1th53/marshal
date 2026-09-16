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
		{"codex auto uses project doc", "codex", injectAuto, injectProjectDoc, false},
		{"codex cannot take a system prompt", "codex", injectSystemPrompt, injectProjectDoc, true},
		{"claude takes a system prompt", "claude", injectSystemPrompt, injectSystemPrompt, false},
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

	// The operator's own system prompt wins; MARSHAL does not stack a second.
	operator := []string{"--append-system-prompt", "mine"}
	args, note, err = applyBriefing("claude", root, operator, "prior work", injectSystemPrompt)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(args) != 2 {
		t.Errorf("operator's own flag was overridden: %q", args)
	}
	if !strings.Contains(note, "skipped") {
		t.Errorf("skip not reported: %q", note)
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

// An operator prompt already occupies the positional slot, so a second one
// would be handed to the CLI as a stray argument.
func TestHasOperatorPrompt(t *testing.T) {
	if !hasOperatorPrompt([]string{"--", "do the thing"}) {
		t.Error("prompt after -- not detected")
	}
	if hasOperatorPrompt([]string{"--continue"}) {
		t.Error("flag-only argv reported as carrying a prompt")
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
