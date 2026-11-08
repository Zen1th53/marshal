//go:build !governed

package app

import (
	"github.com/Zen1th53/marshal/internal/marshal"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateReviewerReceivesCounterAndHistoricalAuthorityMeaning(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "fixture-codex")
	stub := `#!/bin/sh
for arg do prompt="$arg"; done
if [ "$prompt" = "-" ]; then prompt=''; while IFS= read -r line || [ -n "$line" ]; do prompt="$prompt$line
"; done; fi
printf '%s' "$prompt" > captured-prompt
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"Verdict\":\"return\",\"Reviewer\":\"fixture\",\"Reasons\":[\"fixture\"],\"EvidenceRefs\":[]}"}}'
`
	stub = strings.Replace(stub, "> captured-prompt", "> '"+strings.ReplaceAll(filepath.Join(root, "captured-prompt"), "'", "'\"'\"'")+"'", 1)
	stub = strings.Replace(stub, "#!/bin/sh\n", "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"host-review-fixture\"}'\n", 1)
	if err := os.WriteFile(bin, []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	for _, control := range []marshal.Control{marshal.ControlFree, marshal.ControlStrict} {
		cli := &MarshalCLI{Provider: "codex", Binary: bin, Dir: root}
		review, err := cli.Review(requiredMarshalTestContext(t, cli), marshal.Task{}, marshal.HandIn{}, control)
		if err != nil {
			t.Fatal(err)
		}
		prompt, err := os.ReadFile(filepath.Join(root, "captured-prompt"))
		if err != nil {
			t.Fatal(err)
		}
		for _, meaning := range []string{"historical admitted facts", "execution-engine admissions", "UNKNOWN", "not evidence of host tool effects", "WorkerReported"} {
			if !strings.Contains(string(prompt), meaning) {
				t.Fatalf("review context omits %q", meaning)
			}
		}
		if review.Verdict != marshal.VerdictReturn {
			t.Fatal("guidance changed independent verdict")
		}
		if control == marshal.ControlStrict && !strings.Contains(string(prompt), "return it for any departure") {
			t.Fatal("strict instruction contract removed")
		}
	}
}
