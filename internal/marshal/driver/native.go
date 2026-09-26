package driver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// Native runs a worker's own CLI headlessly in the task worktree, under the
// user's own subscription. Its stdin is never a terminal: a Marshal worker
// must not be able to stop and wait for a person, and several must run at
// once, which an interactive session that takes over the terminal cannot do.
type Native struct {
	// Provider names the worker family recorded on the hand-in.
	Provider string
	// Binary is the CLI to run.
	Binary string
	// Args builds the headless command line for one request.
	Args func(Request) []string
	// Parse reads the worker-reported actions from the CLI's JSON stream.
	// What it returns is advisory and never acceptance evidence.
	Parse func(stream []byte) []marshal.CommandRecord
	// CheckTimeout bounds each re-run acceptance check.
	CheckTimeout time.Duration
}

func (Native) Mode() marshal.WorkerMode { return marshal.Native }

// Codex runs `codex exec --json` confined to the worktree.
func Codex(binary string) Native {
	return Native{Provider: "codex", Binary: orDefault(binary, "codex"), Parse: parseCodex, Args: func(r Request) []string {
		args := []string{"exec", "--json", "-C", r.Worktree, "-s", "workspace-write"}
		if r.Model != "" {
			args = append(args, "--model", r.Model)
		}
		return append(args, r.Brief)
	}}
}

// Claude runs Claude Code in print mode with a stream-json transcript. Edits
// are accepted without prompting; commands that would need a person's
// permission are refused by the CLI, which is the correct outcome for a
// worker that has no person attached.
func Claude(binary string) Native {
	return Native{Provider: "claude", Binary: orDefault(binary, "claude"), Parse: parseClaude, Args: func(r Request) []string {
		args := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits"}
		if r.Model != "" {
			args = append(args, "--model", r.Model)
		}
		return append(args, r.Brief)
	}}
}

// Agy runs the Antigravity CLI in print mode with a stream-json transcript.
func Agy(binary string) Native {
	return Native{Provider: "agy", Binary: orDefault(binary, "agy"), Parse: parseAgy, Args: func(r Request) []string {
		args := []string{"-p", r.Brief, "--output-format", "stream-json", "--mode", "accept-edits"}
		if r.Model != "" {
			args = append(args, "--model", r.Model)
		}
		return args
	}}
}

// OpenCode runs `opencode run`. It is also the route to local Ollama models,
// selected with a model of the form "ollama/<tag>"; MARSHAL has no separate
// Ollama execution adapter.
func OpenCode(binary string) Native {
	return Native{Provider: "opencode", Binary: orDefault(binary, "opencode"), Parse: parseNone, Args: func(r Request) []string {
		args := []string{"run", "--format", "json"}
		if r.Model != "" {
			args = append(args, "-m", r.Model)
		}
		return append(args, r.Brief)
	}}
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// Launch starts the CLI in the worktree with no terminal attached.
func (n Native) Launch(ctx context.Context, req Request) (*Handle, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	if n.Args == nil || n.Parse == nil {
		return nil, fmt.Errorf("%w: %s driver is incomplete", ErrInvalidRequest, n.Provider)
	}
	argv := n.Args(req)
	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, n.Binary, argv...)
	cmd.Dir = req.Worktree
	// A nil Stdin reads from the null device: never a terminal.
	cmd.Stdin = nil
	stdout := &capBuffer{limit: maxStream}
	stderr := &capBuffer{limit: maxOutput}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %s: %w", n.Provider, err)
	}
	h := &Handle{req: req, cancel: cancel, done: make(chan struct{})}
	command := n.Binary + " " + strings.Join(argv, " ")
	go func() {
		defer close(h.done)
		err := cmd.Wait()
		code := 0
		if err != nil {
			code = -1
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			}
		}
		h.runErr = err
		h.observed = marshal.CommandRecord{Command: command, ExitCode: code, Output: bound(stdout.String()) + stderr.String()}
		h.reported = n.Parse(stdout.Bytes())
	}()
	return h, nil
}

// Wait assembles the hand-in once the CLI exits. A worker that exits with an
// error still hands in: what it left is judged like any other work.
func (n Native) Wait(ctx context.Context, h *Handle) (marshal.HandIn, error) {
	if err := wait(ctx, h); err != nil {
		return marshal.HandIn{}, err
	}
	id := identity{worker: h.req.Task.Worker, provider: n.Provider, model: h.req.Model, mode: marshal.Native}
	return assemble(ctx, h.req, id, []marshal.CommandRecord{h.observed}, h.reported, n.CheckTimeout)
}

// Cancel stops the CLI and its children. The worktree is not touched.
func (n Native) Cancel(h *Handle) error {
	h.cancel()
	<-h.done
	return nil
}

// eachJSON calls fn for every JSON object line in a stream. Lines that are
// not JSON are skipped: a stream is a claim, not a contract.
func eachJSON(stream []byte, fn func(map[string]any)) {
	scanner := bufio.NewScanner(bytes.NewReader(stream))
	scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			fn(event)
		}
	}
}

// parseCodex reads completed command executions from `codex exec --json`.
func parseCodex(stream []byte) []marshal.CommandRecord {
	var out []marshal.CommandRecord
	eachJSON(stream, func(event map[string]any) {
		if event["type"] != "item.completed" {
			return
		}
		item, _ := event["item"].(map[string]any)
		if item["type"] != "command_execution" {
			return
		}
		command, _ := item["command"].(string)
		output, _ := item["aggregated_output"].(string)
		code := -1
		if n, ok := item["exit_code"].(float64); ok {
			code = int(n)
		}
		out = append(out, marshal.CommandRecord{Command: command, ExitCode: code, Output: bound(output)})
	})
	return out
}

// parseClaude reads tool calls from Claude Code's stream-json transcript.
// The stream does not tie a result's exit status to the call, so the exit
// code is recorded as unknown (-1).
func parseClaude(stream []byte) []marshal.CommandRecord {
	var out []marshal.CommandRecord
	eachJSON(stream, func(event map[string]any) {
		if event["type"] != "assistant" {
			return
		}
		message, _ := event["message"].(map[string]any)
		content, _ := message["content"].([]any)
		for _, block := range content {
			part, _ := block.(map[string]any)
			if part["type"] != "tool_use" {
				continue
			}
			name, _ := part["name"].(string)
			input, _ := json.Marshal(part["input"])
			out = append(out, marshal.CommandRecord{Command: name + " " + string(input), ExitCode: -1})
		}
	})
	return out
}

// parseAgy reads tool steps from the Antigravity CLI's stream-json
// transcript. Only user input and agent text are known step types; every
// other completed step is recorded as a reported action under its type.
func parseAgy(stream []byte) []marshal.CommandRecord {
	var out []marshal.CommandRecord
	eachJSON(stream, func(event map[string]any) {
		if event["event"] != "step_update" {
			return
		}
		step, _ := event["step_update"].(map[string]any)
		kind, _ := step["step_type"].(string)
		if kind == "" || kind == "user_input" || kind == "agent_response" || step["state"] != "DONE" {
			return
		}
		command := kind
		if detail, ok := step["command"].(string); ok && detail != "" {
			command += " " + detail
		}
		out = append(out, marshal.CommandRecord{Command: command, ExitCode: -1})
	})
	return out
}

// parseNone is used where no stream format has been captured to parse
// against. Reporting nothing is honest; guessing a format would not be.
func parseNone([]byte) []marshal.CommandRecord { return nil }
