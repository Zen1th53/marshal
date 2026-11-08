//go:build !governed && linux

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/adapter/opencode"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

type approvedInstructionHostRunner struct{ binary string }

func (r approvedInstructionHostRunner) Run(ctx context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/python3", append([]string{"-I", "-B", r.binary}, command.Args...)...)
	cmd.Dir = command.Dir
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(command.Stdin)
	start := time.Now().UTC()
	out, err := cmd.Output()
	return adapter.ProcessResult{Stdout: out, StartedAt: start, EndedAt: time.Now().UTC()}, err
}

func TestApprovedInstructionsReachFourOwnedCLICommandInputs(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	// This wholly owned fixture executable captures actual adapter argv/stdin.
	// It implements local version/help/result fixtures only: no provider binary,
	// credential, network, repository writes, or real worker authority is used.
	capture := filepath.Join(t.TempDir(), "command.json")
	encodedCapture, _ := json.Marshal(capture)
	script := "#!/usr/bin/python3 -I -B\nimport json,sys\nargs=sys.argv[1:]\nif args==['--version']:\n print('1.2.14')\nelif args==['--help']:\n print('--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model')\nelse:\n with open(" + string(encodedCapture) + ",'x') as f:\n  json.dump({'args':args,'stdin':sys.stdin.read()},f)\n print(json.dumps({'status':'SUCCESS','response':'owned fixture done','result':'owned fixture done'}))\n"
	binary := filepath.Join(t.TempDir(), "fake-cli")
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runner := approvedInstructionHostRunner{binary: binary}
	p, run, task := approvedInstructionFixture()
	trusted, err := approvedExecutionTaskInstructions(p, run, task, "Required host context\n", 16384)
	if err != nil {
		t.Fatal(err)
	}
	request := adapter.Request{TaskID: "TASK-owned", Title: task.Description, Worktree: root, Model: "fixture-model", TrustedContext: trusted}
	clients := map[string]adapter.Adapter{
		"codex":    codex.NewWithModels(binary, runner, []codex.ModelInfo{{Slug: request.Model}}, request.Model),
		"claude":   claude.New(binary, runner),
		"opencode": opencode.NewWithModel(binary, runner, request.Model),
		"agy":      antigravity.NewGoverned(binary, runner),
	}
	for _, provider := range []string{"codex", "claude", "opencode", "agy"} {
		t.Run(provider, func(t *testing.T) {
			defer os.Remove(capture)
			ctx := t.Context()
			if provider == "agy" {
				ctx, err = sessionmcp.WithBoundWorker(ctx, root, request.TaskID, func(context.Context, io.Reader, io.Writer) error { return nil })
				if err != nil {
					t.Fatal(err)
				}
				if _, err = clients[provider].Probe(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = clients[provider].Run(ctx, request); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			var observed struct {
				Args  []string `json:"args"`
				Stdin string   `json:"stdin"`
			}
			if err = json.Unmarshal(data, &observed); err != nil {
				t.Fatal(err)
			}
			payload := strings.TrimSpace(observed.Stdin)
			if provider != "codex" {
				wire, err := requiredProcess05ProviderPrompt(provider, request)
				if err != nil {
					t.Fatal(err)
				}
				for _, arg := range observed.Args {
					if arg == string(wire) {
						payload = arg
					}
				}
			}
			if index := strings.IndexByte(payload, '{'); index >= 0 {
				payload = payload[index:]
			}
			var envelope struct {
				TrustedContext string `json:"trusted_context"`
				Title          string `json:"title"`
			}
			if err = json.Unmarshal([]byte(payload), &envelope); err != nil {
				t.Fatal("actual adapter command omitted JSON task input", err)
			}
			if envelope.TrustedContext != trusted || envelope.Title != task.Description || !strings.Contains(envelope.TrustedContext, p.Tasks[0].Instructions) || !strings.Contains(envelope.TrustedContext, p.Tasks[0].ExpectedOutput) {
				t.Fatal("actual owned adapter command changed/omitted approved instructions")
			}
			if err = os.Remove(capture); err != nil {
				t.Fatal(err)
			}
		})
	}
}
