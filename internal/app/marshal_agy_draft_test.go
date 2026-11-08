package app

import (
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/marshal"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fourProviderMarshalDraft(t *testing.T) []byte {
	t.Helper()
	bin := t.TempDir()
	for _, worker := range []string{"codex", "claude", "opencode", "agy"} {
		if err := os.WriteFile(filepath.Join(bin, worker), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	data, err := os.ReadFile(filepath.Join("testdata", "agy_four_provider_marshal_draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMarshalDraftPromptMatchesPrivateAGYAdmission(t *testing.T) {
	data := fourProviderMarshalDraft(t)
	bin := os.Getenv("PATH")
	captured := filepath.Join(bin, "captured-prompt")
	event, err := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": string(data)}})
	if err != nil {
		t.Fatal(err)
	}
	quoted := strings.ReplaceAll(string(event), "'", "'\"'\"'")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"host-draft-fixture\"}'\nlast=''; for arg; do last=$arg; done; if [ \"$last\" = '-' ]; then while IFS= read -r line || [ -n \"$line\" ]; do printf '%s\\n' \"$line\"; done > '" + captured + "'; else printf '%s\\n' \"$@\" > '" + captured + "'; fi\nprintf '%s\\n' '" + quoted + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cli := &MarshalCLI{Provider: "codex", Binary: filepath.Join(bin, "codex"), Dir: bin, ProjectID: "PROJECT-11111111111111111111111111111111"}
	_, draftErr := cli.Draft(requiredMarshalTestContext(t, cli), "governed four-provider marker qualification")
	prompt, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	if privateAGYEnabled() {
		if draftErr != nil || !strings.Contains(string(prompt), "agy also supports governed when requested") || strings.Contains(string(prompt), "agy uses native") {
			t.Fatalf("private prompt/admission conflict: %v", draftErr)
		}
	} else if !strings.Contains(string(prompt), "agy uses native") || draftErr == nil {
		t.Fatalf("Community admission/prompt changed: %v", draftErr)
	}
	cli = &MarshalCLI{Provider: "codex", Binary: filepath.Join(bin, "codex"), Dir: bin, ProjectID: "PROJECT-11111111111111111111111111111111"}
	_, amendErr := cli.Amend(requiredMarshalTestContext(t, cli), marshal.Run{PlanVersion: 1}, "preserve existing modes")
	prompt, err = os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	if privateAGYEnabled() {
		if amendErr != nil || !strings.Contains(string(prompt), "opencode and agy also support governed when requested") || strings.Contains(string(prompt), "other workers use native") {
			t.Fatalf("private amendment prompt conflict: %v", amendErr)
		}
	} else if !strings.Contains(string(prompt), "other workers use native") || amendErr == nil {
		t.Fatalf("Community amendment behavior changed: %v", amendErr)
	}

}
