package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

type Client struct {
	binary       string
	runner       adapter.ProcessRunner
	version      string
	models       []ModelInfo
	defaultModel string
}

func New(binary string, runner adapter.ProcessRunner) *Client {
	return &Client{binary: binary, runner: runner}
}

// Binary exposes the resolved executable path so the runtime can start the
// governed stream lifecycle without re-resolving it from PATH.
func (c *Client) Binary() string {
	if c == nil {
		return ""
	}
	return c.binary
}

// Runner exposes the sandboxed process runner bound to this client.
func (c *Client) Runner() adapter.ProcessRunner {
	if c == nil {
		return nil
	}
	return c.runner
}

func (c *Client) Probe(ctx context.Context) (adapter.Probe, error) {
	if c.binary == "" || c.runner == nil {
		return adapter.Probe{}, fmt.Errorf("%w: Claude binary and process runner are required", model.ErrInvalid)
	}
	// A probe shares the host with every lane opening at the same time; under
	// that load the sandboxed runner can take seconds just to start.
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := probeCtx.Err(); err != nil {
		return adapter.Probe{}, fmt.Errorf("%w: probe Claude cancelled: %w", model.ErrUnavailable, err)
	}
	// Leave half the aggregate deadline for help, while capping each phase so a
	// slow version command cannot consume the help budget.
	deadline, _ := probeCtx.Deadline()
	versionBudget := min(15*time.Second, time.Until(deadline)/2)
	runPhase := func(flag string, budget time.Duration) (adapter.ProcessResult, error) {
		phaseCtx, phaseCancel := context.WithTimeout(probeCtx, budget)
		defer phaseCancel()
		if err := phaseCtx.Err(); err != nil {
			return adapter.ProcessResult{}, fmt.Errorf("%w: probe Claude %s failed: %w", model.ErrUnavailable, flag, err)
		}
		result, runErr := c.runner.Run(phaseCtx, adapter.Command{Path: c.binary, Args: []string{flag}})
		if err := errors.Join(runErr, phaseCtx.Err()); err != nil {
			return adapter.ProcessResult{}, fmt.Errorf("%w: probe Claude %s failed: %w", model.ErrUnavailable, flag, err)
		}
		if result.ExitCode != 0 || result.TimedOut || result.Cancelled || result.OutputTruncated {
			return adapter.ProcessResult{}, fmt.Errorf("%w: probe Claude %s failed (exit=%d timeout=%t cancelled=%t truncated=%t)",
				model.ErrUnavailable, flag, result.ExitCode, result.TimedOut, result.Cancelled, result.OutputTruncated)
		}
		return result, nil
	}
	versionRes, err := runPhase("--version", versionBudget)
	if err != nil {
		return adapter.Probe{}, err
	}
	helpRes, err := runPhase("--help", 15*time.Second)
	if err != nil {
		return adapter.Probe{}, err
	}
	// The governed stream lifecycle depends on every one of these flags. A
	// build without them would silently fall back to a weaker surface, so
	// refuse here rather than discover it mid-turn.
	help := string(helpRes.Stdout) + string(helpRes.Stderr)
	for _, required := range []string{
		"--output-format", "--permission-mode", "--permission-prompts",
		"--strict-mcp-config", "--add-dir", "--resume",
	} {
		if !strings.Contains(help, required) {
			return adapter.Probe{}, fmt.Errorf("%w: Claude CLI lacks required flag %s", model.ErrUnavailable, required)
		}
	}
	c.version = strings.TrimSpace(string(versionRes.Stdout))

	return adapter.Probe{
		Name: "claude", Available: true, Version: c.version, Capabilities: c.Capabilities(),
	}, nil
}

func (c *Client) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	if c.runner == nil || c.binary == "" || request.TaskID == "" || request.Title == "" || request.Worktree == "" {
		return adapter.Result{}, fmt.Errorf("%w: incomplete Claude run request", model.ErrInvalid)
	}
	if request.Model != "" {
		if request.Model != strings.TrimSpace(request.Model) || !isNativeIdentifier(request.Model) {
			return adapter.Result{}, fmt.Errorf("%w: invalid Claude model selector", model.ErrInvalid)
		}
		if err := ValidateDangerousFlags([]string{request.Model}); err != nil {
			return adapter.Result{}, err
		}
	}
	prompt, err := buildPrompt(request)
	if err != nil {
		return adapter.Result{}, err
	}
	// Run is the governed worker path: MARSHAL supplies confinement and
	// approval/merge review. Native sessions use a separate launcher.
	args := []string{
		"-p", string(prompt), "--output-format", "json",
		"--permission-mode", "bypassPermissions", "--permission-prompts", "none",
	}
	if request.Model != "" {
		if err := ValidateDangerousFlags([]string{request.Model}); err != nil {
			return adapter.Result{}, err
		}
		args = append(args, "--model", request.Model)
	}
	args, env, cleanup, err := sessionmcp.Configure(ctx, "claude", args, nil)
	if err != nil {
		return adapter.Result{}, err
	}
	defer cleanup()
	process, err := c.runner.Run(ctx, adapter.Command{
		Path: c.binary, Args: args, Env: env,
		Dir:               request.Worktree,
		Heartbeat:         request.Heartbeat,
		HeartbeatInterval: request.HeartbeatInterval,
		Liveness:          quietWorkerLiveness(request),
	})
	if err != nil {
		return adapter.Result{}, fmt.Errorf("run Claude process: %w", err)
	}
	result := adapter.Result{
		Adapter: "claude", AdapterVersion: c.version, ExitCode: process.ExitCode,
		Stdout: process.Stdout, Stderr: process.Stderr, StartedAt: process.StartedAt,
		EndedAt: process.EndedAt, TimedOut: process.TimedOut,
		Cancelled: process.Cancelled, OutputTruncated: process.OutputTruncated,
		Isolation:        process.Isolation,
		Status:           adapter.StatusSuccess,
		StoppedAfterDone: process.StoppedAfterDone, IdleTimedOut: process.IdleTimedOut,
	}
	if process.StoppedAfterDone && !process.TimedOut && !process.Cancelled {
		result.ExitCode = 0 // the final result event was observed
	}
	if result.ExitCode != 0 || process.TimedOut || process.Cancelled || process.IdleTimedOut {
		result.Status = adapter.StatusFailure
	} else if process.OutputTruncated {
		result.Status = adapter.StatusBlocked
	}
	parseClaudeOutput(process.Stdout, &result)
	return result, nil
}

func (c *Client) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusBlocked, fmt.Errorf("%w: Claude status is runtime-managed", model.ErrUnavailable)
}

func (c *Client) Resume(context.Context, string, adapter.Request) (adapter.Result, error) {
	return adapter.Result{}, fmt.Errorf("%w: Claude resume is not exposed by Runtime", model.ErrUnavailable)
}

func (c *Client) Capabilities() map[string]string {
	return map[string]string{
		"run": "native", "sandbox": "native", "resume": "emulated",
		"status": "emulated", "evidence": "emulated",
	}
}

func (c *Client) CollectEvidence(result adapter.Result) map[string]any {
	return map[string]any{
		"adapter": result.Adapter, "adapter_version": result.AdapterVersion,
		"session_id": result.SessionID, "exit_code": result.ExitCode,
		"started_at": result.StartedAt, "ended_at": result.EndedAt,
		"timed_out": result.TimedOut, "output_truncated": result.OutputTruncated,
	}
}

func (c *Client) Shutdown(context.Context, string) error {
	return nil
}

// GovernedPrompt renders the exact data envelope used by this adapter. It
// performs no provider call and conveys no execution or approval authority.
func GovernedPrompt(request adapter.Request) ([]byte, error) { return buildPrompt(request) }

func buildPrompt(request adapter.Request) ([]byte, error) {
	envelope := struct {
		TaskID            string   `json:"task_id"`
		Title             string   `json:"title"`
		Worktree          string   `json:"worktree"`
		BaseCommit        string   `json:"base_commit"`
		HeadCommit        string   `json:"head_commit"`
		AllowedOperations []string `json:"allowed_operations"`
		EvidenceRequired  []string `json:"evidence_required"`
		TrustedContext    string   `json:"trusted_context,omitempty"`
		Rules             []string `json:"rules"`
	}{
		TaskID: request.TaskID, Title: request.Title, Worktree: request.Worktree,
		BaseCommit: request.BaseCommit, HeadCommit: request.HeadCommit,
		AllowedOperations: request.AllowedOperations,
		EvidenceRequired:  request.EvidenceRequired,
		TrustedContext:    request.TrustedContext,
		Rules: []string{
			"Work only inside the assigned worktree.",
			"Do not push, rewrite history, deploy, upload externally, or access secrets.",
			"Do not issue QA or AppSec approval.",
			"Prepare reviewable changes; the runtime creates the task commit.",
		},
	}
	payload, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Claude task prompt: %w", err)
	}
	return append([]byte("Execute this MARSHAL task envelope:\n"), payload...), nil
}

// ClassifyStreamLine treats Claude's successful final result event as done.
func ClassifyStreamLine(line []byte) adapter.StreamSignal {
	var event struct {
		Type    string `json:"type"`
		IsError bool   `json:"is_error"`
	}
	if json.Unmarshal(line, &event) == nil && event.Type == "result" && !event.IsError {
		return adapter.SignalDone
	}
	// Verbose json output prints every event as one array; its last element
	// is the final result event.
	var batch []struct {
		Type    string `json:"type"`
		IsError bool   `json:"is_error"`
	}
	if json.Unmarshal(line, &batch) == nil && len(batch) > 0 && batch[len(batch)-1].Type == "result" && !batch[len(batch)-1].IsError {
		return adapter.SignalDone
	}
	return adapter.SignalNone
}

func parseClaudeOutput(output []byte, result *adapter.Result) {
	events := DecodeOutputEvents(output)
	finalFromResult := false
	for _, event := range events {
		result.Events = append(result.Events, event)
		if sessID, ok := event["session_id"].(string); ok && sessID != "" {
			result.SessionID = sessID
		}
		resText, ok := event["result"].(string)
		if !ok || resText == "" {
			continue
		}
		// The final result event names the reply; an earlier event carrying
		// a "result" string never overrides it.
		if event["type"] == "result" {
			result.FinalText, finalFromResult = resText, true
		} else if !finalFromResult {
			result.FinalText = resText
		}
	}
	if result.FinalText == "" && len(output) > 0 {
		result.FinalText = string(output)
	}
}

// DecodeOutputEvents reads Claude print output in each shape the CLI emits:
// one JSON object (--output-format json), one JSON array of every event
// (--output-format json with verbose on, e.g. "verbose": true in the user's
// settings), or one JSON object per line (stream-json). Lines that are not
// JSON are skipped.
func DecodeOutputEvents(output []byte) []map[string]any {
	var events []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		switch {
		case len(line) > 0 && line[0] == '[':
			var batch []map[string]any
			if json.Unmarshal(line, &batch) == nil {
				events = append(events, batch...)
			}
		case len(line) > 0 && line[0] == '{':
			var event map[string]any
			if json.Unmarshal(line, &event) == nil {
				events = append(events, event)
			}
		}
	}
	if len(events) == 0 {
		// A pretty-printed document spans lines; decode it whole.
		trimmed := bytes.TrimSpace(output)
		var batch []map[string]any
		var event map[string]any
		if json.Unmarshal(trimmed, &batch) == nil {
			events = batch
		} else if json.Unmarshal(trimmed, &event) == nil {
			events = []map[string]any{event}
		}
	}
	return events
}

// quietWorkerLiveness keeps the completion event and its grace but drops the
// idle stop: with --output-format json Claude prints nothing until it ends,
// so silence is no sign of a stuck worker and must not stop it.
func quietWorkerLiveness(request adapter.Request) *adapter.Liveness {
	live := adapter.WorkerLiveness(request, ClassifyStreamLine)
	live.StallAfter, live.IdleTimeout, live.Stalled = 0, 0, nil
	return live
}
