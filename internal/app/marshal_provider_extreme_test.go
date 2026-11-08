//go:build !governed

package app

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExtremeOpenCodeMarshalStructuredTurnAndResume(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "args")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MARSHAL_TEST_ARGS\"\ncat <<'EOF'\n{\"type\":\"step_start\",\"sessionID\":\"ses-marshal\"}\n{\"type\":\"text\",\"sessionID\":\"ses-marshal\",\"part\":{\"text\":\"{\\\"ok\\\":true}\"}}\nEOF\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_TEST_ARGS", log)
	if !slices.Contains(MarshalProviders(), "opencode") || !slices.Contains(installedMarshalProviders(), "opencode") {
		t.Fatal("OpenCode not eligible")
	}
	m := &MarshalCLI{Provider: "opencode", Dir: root}
	ctx := requiredMarshalTestContext(t, m)
	for _, resume := range []bool{false, true} {
		var out struct {
			OK bool `json:"ok"`
		}
		if err := m.turn(ctx, "Review only.", `{"type":"object"}`, &out); err != nil || !out.OK {
			t.Fatalf("turn: %+v %v", out, err)
		}
		if m.ConversationID != "ses-marshal" {
			t.Fatalf("binding: %s", m.ConversationID)
		}
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.SplitN(strings.TrimSuffix(string(data), "\n"), "\n", 6)
		want := []string{"run", "--format", "json", "--agent", "plan"}
		if resume {
			// Resume selectors precede the one complete multi-line prompt.
			args = strings.SplitN(strings.TrimSuffix(string(data), "\n"), "\n", 8)
			want = append(want, "--session", "ses-marshal")
		}
		if len(args) != len(want)+1 {
			t.Fatal("actual OpenCode command omitted prompt/schema")
		}
		actualPrompt := requiredMarshalTestPrompt(t, m, "Review only.", args[len(args)-1])
		if !strings.HasSuffix(actualPrompt, `Return only JSON matching this schema: {"type":"object"}`) {
			t.Fatal("exact schema missing from native command")
		}
		want = append(want, actualPrompt)
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("args: %q want %q", args, want)
		}
	}
}

func TestExtremeOpenCodeMarshalOutputRefusesMalformed(t *testing.T) {
	for _, data := range []string{"", "not json", `{"type":"step_start","sessionID":"ses"}`} {
		if _, _, err := marshalCLIOutput("opencode", []byte(data)); err == nil {
			t.Fatalf("accepted invalid output %q", data)
		}
	}
}
