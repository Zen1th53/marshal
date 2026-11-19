package tui

import (
	"slices"
	"testing"
)

func TestMarshalChatEffortArgs(t *testing.T) {
	for _, tc := range []struct {
		name, provider, effort string
		args, want             []string
	}{
		{"default", "codex", "", []string{"Hello."}, []string{"-c", `model_reasoning_effort="high"`, "Hello."}},
		{"saved effort", "codex", "low", nil, []string{"-c", `model_reasoning_effort="low"`}},
		{"config", "codex", "high", []string{"-c", `model_reasoning_effort="low"`, "Hello."}, []string{"-c", `model_reasoning_effort="low"`, "Hello."}},
		{"long config", "codex", "", []string{"--config=model_reasoning_effort=medium"}, []string{"--config=model_reasoning_effort=medium"}},
		{"profile", "codex", "", []string{"--profile", "owner"}, []string{"--profile", "owner"}},
		{"claude", "claude", "", []string{"Hello."}, []string{"Hello."}},
		{"agy", "antigravity", "", []string{"--prompt-interactive", "Hello."}, []string{"--prompt-interactive", "Hello."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalChatEffortArgs(tc.provider, "", slices.Clone(tc.args), tc.effort)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("args = %q, want %q", got, tc.want)
			}
			if again := marshalChatEffortArgs(tc.provider, "", got, ""); !slices.Equal(again, got) {
				t.Fatalf("effort added twice: %q", again)
			}
		})
	}
}

func TestPrepareMarshalLaunchCodexEffort(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, effort := range []string{"", "low"} {
			var base []string
			if resume {
				base = []string{"resume", "session-id"}
			}
			if effort != "" {
				base = marshalChatEffortArgs("codex", "", base, effort)
			} else {
				effort = "high"
			}
			args, _, dir, err := prepareMarshalLaunch("codex", t.TempDir(), base, "MARSHAL PROTOCOL\nPlan with the owner.", resume, "")
			if dir != nil {
				t.Cleanup(dir.remove)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(args, `model_reasoning_effort="`+effort+`"`) {
				t.Fatalf("launch lost effort %q: %q", effort, args)
			}
			if !resume && args[len(args)-1] != marshalKickoff {
				t.Fatalf("opening cue moved: %q", args)
			}
		}
	}
}
