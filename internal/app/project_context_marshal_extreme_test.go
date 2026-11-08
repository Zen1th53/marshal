//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestMarshalModelRequiredContextNeedsAuthenticatedCurrentOwnerAndFullPacket(t *testing.T) {
	r, b, record, _ := requiredContextFixture(t, true)
	request := planCreateRequest()
	request.SessionID, request.ProjectID = b.Session, projectid.ID(r.ProjectIdentity())
	if _, err := r.Plans().Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	m := &MarshalCLI{Provider: "codex", Dir: r.ProjectRoot(), ProjectID: r.ProjectIdentity(), runtime: r}
	if _, _, _, err := m.prepareRequiredMarshalModelTurn(t.Context(), "Read-only planning"); err == nil {
		t.Fatal("unbound context authorised a planning model")
	}
	ctx := control.Context(t.Context())
	owned, prompt, finish, err := m.prepareRequiredMarshalModelTurn(ctx, "Read-only planning")
	if err != nil || !strings.Contains(prompt, record.Body) || !strings.Contains(prompt, "Preserve reliable project execution") {
		t.Fatal("planning omitted required task-only policy or architecture", err)
	}
	defer finish()
	if _, err := r.store.UpdateMemory(t.Context(), record.ProjectID, record.ID, record.Revision, func(rec *model.MemoryRecordV2) error { rec.ACLScope = "foreign"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := finish(); err == nil {
		t.Fatal("reviewer accepted output after required context was revoked")
	}
	if owned.Err() == nil || ctx.Err() != nil {
		t.Fatal("required model context did not cancel only its owned child")
	}
	if _, _, _, err := (&MarshalCLI{Provider: "codex", Dir: r.ProjectRoot()}).prepareRequiredMarshalModelTurn(context.Background(), "Unbound call"); err == nil {
		t.Fatal("standalone private model bypassed mandatory context")
	}
}

// Runs an actual subprocess through MarshalCLI.turn, with a host-owned script
// instead of any model or native approval source. Capture files are outside the
// inspected repository, so they cannot change its required source tree.
func TestMarshalModelRequiredContextActualTurnBoundary(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("host subprocess test requires python3")
	}
	for _, scenario := range []string{"complete", "unbound", "oversized-schema", "revoked-during-turn", "foreign-repository", "malformed-result", "binary-changed-during-turn"} {
		t.Run(scenario, func(t *testing.T) {
			outside := t.TempDir()
			capture := filepath.Join(outside, "prompt")
			binary := filepath.Join(outside, "host-owned-cli")
			held := scenario == "revoked-during-turn"
			payload := `{"ok":true}`
			if scenario == "malformed-result" {
				payload = `{"ok":true,"unreviewed_field":"forbidden"}`
			}
			script := fmt.Sprintf("#!%s\nimport json,sys,time\nprompt=sys.stdin.read() if sys.argv[-1]=='-' else sys.argv[-1]\nwith open(%q,'w') as f: f.write(prompt)\nif %s: time.sleep(5)\nprint(json.dumps({'type':'thread.started','thread_id':'host-test-session'}))\nprint(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':%q}}))\n", python, capture, map[bool]string{true: "True", false: "False"}[held], payload)
			if scenario == "binary-changed-during-turn" {
				script = strings.Replace(script, "print(json.dumps", "with open(__file__, 'a') as f: f.write('\\n# changed during invocation\\n')\nprint(json.dumps", 1)
			}
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			m := &MarshalCLI{Provider: "codex", Binary: binary, ConversationID: "previous-session"}
			if scenario == "complete" {
				m.ConversationID = ""
			}
			ctx := requiredMarshalTestContext(t, m)
			schema := `{"type":"object","properties":{"ok":{"type":"boolean"}}}`
			switch scenario {
			case "unbound":
				m.runtime = nil
			case "oversized-schema":
				schema = strings.Repeat("s", requiredProviderEnvelopeBytes)
			case "foreign-repository":
				foreign := runtimeRepo(t)
				m.Dir = foreign.Path()
			}
			var output struct {
				OK bool `json:"ok"`
			}
			result := make(chan error, 1)
			go func() { result <- m.turn(ctx, "Keep the required project architecture", schema, &output) }()
			if held {
				deadline := time.NewTimer(3 * time.Second)
				defer deadline.Stop()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					if _, err := os.Stat(capture); err == nil {
						break
					}
					select {
					case err := <-result:
						t.Fatal("turn ended before hosted invocation", err)
					case <-deadline.C:
						t.Fatal("hosted invocation did not start")
					case <-ticker.C:
					}
				}
				if _, err := m.runtime.store.UpdateMemory(t.Context(), m.runtime.ProjectID(), "MEM-required-resolver", 1, func(rec *model.MemoryRecordV2) error { rec.ACLScope = "foreign"; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-result:
				if scenario == "complete" {
					if err != nil || !output.OK || m.ConversationID != "host-test-session" {
						t.Fatal("complete required turn refused", err)
					}
					prompt, err := os.ReadFile(capture)
					if err != nil || !strings.Contains(string(prompt), "Preserve original accepted history and evidence.") || !strings.Contains(string(prompt), "Preserve reliable project execution") {
						t.Fatal("actual command omitted mandatory task-only memory", err)
					}
					if _, err := m.takeRequiredMarshalTurnCapture(&Runtime{}); err == nil {
						t.Fatal("foreign runtime consumed turn proof")
					}
					proof, err := m.takeRequiredMarshalTurnCapture(m.runtime)
					if err != nil || proof.preparation.receipt.Binding.TaskID != "" || proof.inputDigest == "" || proof.outputDigest == "" || proof.session != "host-test-session" || !strings.HasPrefix(proof.binaryDigest, "sha256:") || proof.observedModel != "" {
						t.Fatal("actual successful turn did not retain provenance", err)
					}
					wire, err := json.Marshal(proof)
					if err != nil || string(wire) != "{}" {
						t.Fatal("live turn proof serialized", string(wire), err)
					}
					if _, err := m.takeRequiredMarshalTurnCapture(m.runtime); err == nil {
						t.Fatal("turn proof replayed")
					}
				} else {
					if err == nil || output.OK || m.ConversationID != "previous-session" {
						t.Fatal("unqualified result adopted", err)
					}
					if scenario == "oversized-schema" && !errors.Is(err, projectcontext.ErrBudget) {
						t.Fatal("wrong envelope refusal", err)
					}
					if m.requiredTurnCapture != nil {
						t.Fatal("refused turn retained a successful proof")
					}
					if !held && scenario != "malformed-result" && scenario != "binary-changed-during-turn" {
						if _, err := os.Stat(capture); !os.IsNotExist(err) {
							t.Fatal("refused turn spawned a command", err)
						}
					}
				}
			case <-time.After(4 * time.Second):
				t.Fatal("required turn did not settle")
			}
			if ctx.Err() != nil {
				t.Fatal("required turn cancelled parent owner context")
			}
		})
	}
}
