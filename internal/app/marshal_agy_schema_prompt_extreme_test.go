//go:build !governed

package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The owned host script verifies what the real CLI invocation receives. This
// is not a claim that an external model obeyed the schema or was called.
func TestMarshalAGYPromptSuppliesExactNativeSchema(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("requires host python3")
	}
	root := t.TempDir()
	captured := filepath.Join(root, "actual-prompt")
	pathJSON, _ := json.Marshal(captured)
	output, _ := json.Marshal(map[string]any{
		"conversation_id": "owned-host-agy",
		"response":        `{"Verdict":"return","Reviewer":"host","Reasons":["Explicit host observation"],"EvidenceRefs":["diff"]}`,
	})
	outputJSON, _ := json.Marshal(string(output))
	script := "#!" + python + "\nimport sys\nwith open(" + string(pathJSON) + ", 'w') as f:f.write(sys.argv[sys.argv.index('-p')+1])\nprint(" + string(outputJSON) + ")\n"
	bin := filepath.Join(root, "agy")
	if err = os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cli := &MarshalCLI{Provider: "agy", Binary: bin, Dir: root}
	var review marshal.Review
	if err = cli.turn(requiredMarshalTestContext(t, cli), "Review the exact hand-in", marshalReviewSchema, &review); err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile(captured)
	if err != nil || !strings.Contains(string(prompt), marshalReviewSchema) || !strings.Contains(string(prompt), "bare JSON object") || !strings.Contains(string(prompt), "exact spelling") {
		t.Fatal("actual AGY prompt omitted its exact structured-result contract", err)
	}
	if review.Verdict != marshal.VerdictReturn || cli.ConversationID != "owned-host-agy" {
		t.Fatal("owned host response was not decoded", review.Verdict)
	}
}
