package codex

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

func NewWithModels(binary string, runner adapter.ProcessRunner, models []ModelInfo, defaultModel string) *Client {
	return &Client{
		binary:       binary,
		runner:       runner,
		models:       models,
		defaultModel: defaultModel,
	}
}

func (c *Client) Binary() string {
	if c == nil {
		return ""
	}
	return c.binary
}

func (c *Client) Runner() adapter.ProcessRunner {
	if c == nil {
		return nil
	}
	return c.runner
}

func (c *Client) Probe(ctx context.Context) (adapter.Probe, error) {
	return c.probe(ctx, 20*time.Second, 5*time.Second)
}

// Each required command gets a fresh startup budget. The total probe budget
// remains finite (45 seconds in production) and is bounded by the caller.
func (c *Client) probe(ctx context.Context, metadataBudget, catalogBudget time.Duration) (adapter.Probe, error) {
	if c.binary == "" || c.runner == nil {
		return adapter.Probe{}, fmt.Errorf("%w: Codex binary and process runner are required", model.ErrInvalid)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*metadataBudget+catalogBudget)
	defer cancel()
	versionResult, err := c.probeCommand(probeCtx, metadataBudget, "version", []string{"--version"})
	if err != nil {
		return adapter.Probe{}, err
	}
	helpResult, err := c.probeCommand(probeCtx, metadataBudget, "exec", []string{"exec", "--help"})
	if err != nil {
		return adapter.Probe{}, err
	}
	version := string(versionResult.Stdout)
	help := string(helpResult.Stdout)
	for _, required := range []string{"--json", "--sandbox", "--ephemeral", "--ignore-user-config", "--cd"} {
		if !strings.Contains(help, required) {
			return adapter.Probe{}, fmt.Errorf("%w: Codex exec lacks required flag %s", model.ErrUnavailable, required)
		}
	}
	c.version = strings.TrimSpace(version)
	if len(c.models) == 0 {
		catalogCtx, catalogCancel := context.WithTimeout(probeCtx, catalogBudget)
		defer catalogCancel()
		if models, def, mErr := DiscoverModels(catalogCtx, c.binary, c.runner); mErr == nil && len(models) > 0 {
			c.models = models
			c.defaultModel = def
		}
	}
	return adapter.Probe{
		Name: "codex", Available: true, Version: c.version, Capabilities: c.Capabilities(),
	}, nil
}

// Keep runner errors (including real capability denials) and context causes
// discoverable even when the governed runner masks cancellation as a denial.
func (c *Client) probeCommand(ctx context.Context, budget time.Duration, name string, args []string) (adapter.ProcessResult, error) {
	commandCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if err := commandCtx.Err(); err != nil {
		return adapter.ProcessResult{}, fmt.Errorf("%w: probe Codex %s failed: %w", model.ErrUnavailable, name, err)
	}
	result, err := c.runner.Run(commandCtx, adapter.Command{Path: c.binary, Args: args})
	cause := errors.Join(err, commandCtx.Err())
	if cause != nil {
		return adapter.ProcessResult{}, fmt.Errorf("%w: probe Codex %s failed: %w", model.ErrUnavailable, name, cause)
	}
	if result.ExitCode != 0 {
		return adapter.ProcessResult{}, fmt.Errorf("%w: probe Codex %s failed: exit code %d", model.ErrUnavailable, name, result.ExitCode)
	}
	return result, nil
}

// Models returns the probed eligible model catalog and discovered default.
func (c *Client) Models(ctx context.Context) ([]ModelInfo, string, error) {
	if len(c.models) > 0 {
		return c.models, c.defaultModel, nil
	}
	models, def, err := DiscoverModels(ctx, c.binary, c.runner)
	if err != nil {
		return nil, "", err
	}
	c.models = models
	c.defaultModel = def
	return models, def, nil
}

// ValidateModel checks if a model exists in the catalog.
func (c *Client) ValidateModel(ctx context.Context, modelName string) error {
	if modelName == "" {
		return nil
	}
	if err := ValidateDangerousFlags([]string{modelName}); err != nil {
		return err
	}
	models, _, err := c.Models(ctx)
	if err != nil {
		return fmt.Errorf("%w: cannot query eligible codex models: %v", model.ErrUnavailable, err)
	}
	for _, m := range models {
		if m.Slug == modelName {
			return nil
		}
	}
	return fmt.Errorf("%w: model %q is not an eligible Codex model", model.ErrInvalid, modelName)
}

// ValidateEffort checks that the catalog advertises effort for the model, or
// for the catalog default when modelName is empty.
func (c *Client) ValidateEffort(ctx context.Context, modelName, effort string) error {
	if effort == "" {
		return nil
	}
	if err := ValidateDangerousFlags([]string{modelName, effort}); err != nil {
		return err
	}
	models, def, err := c.Models(ctx)
	if err != nil {
		return fmt.Errorf("%w: cannot query codex reasoning efforts: %v", model.ErrUnavailable, err)
	}
	if modelName == "" {
		modelName = def
	}
	for _, m := range models {
		if m.Slug != modelName {
			continue
		}
		for _, advertised := range m.ReasoningEfforts {
			if advertised == effort {
				return nil
			}
		}
		return fmt.Errorf("%w: model %q does not advertise reasoning effort %q (advertised: %s)", model.ErrInvalid, modelName, effort, strings.Join(m.ReasoningEfforts, ", "))
	}
	return fmt.Errorf("%w: model %q is not an eligible Codex model", model.ErrInvalid, modelName)
}

func (c *Client) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	if c.runner == nil || c.binary == "" || request.TaskID == "" ||
		request.Title == "" || request.Worktree == "" {
		return adapter.Result{}, fmt.Errorf("%w: incomplete Codex run request", model.ErrInvalid)
	}

	if err := ValidateDangerousFlags([]string{request.Model, request.Title, request.Worktree}); err != nil {
		return adapter.Result{}, err
	}

	effectiveModel := request.Model
	if effectiveModel != "" {
		if err := c.ValidateModel(ctx, effectiveModel); err != nil {
			return adapter.Result{}, err
		}
	} else if c.defaultModel != "" {
		effectiveModel = c.defaultModel
	} else if _, def, err := c.Models(ctx); err == nil && def != "" {
		effectiveModel = def
	}
	if err := c.ValidateEffort(ctx, effectiveModel, request.Effort); err != nil {
		return adapter.Result{}, err
	}

	prompt, err := buildPrompt(request)
	if err != nil {
		return adapter.Result{}, err
	}
	args := []string{
		"exec", "--json", "-C", request.Worktree,
		"--ephemeral", "--ignore-user-config",
	}
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	if request.Effort != "" {
		args = append(args, "-c", `model_reasoning_effort="`+request.Effort+`"`)
	}
	if err := ValidateDangerousFlags(args); err != nil {
		return adapter.Result{}, err
	}

	// Run is the governed worker path. MARSHAL supplies the sandbox and
	// approval boundary; validate caller-controlled args before adding this
	// fixed external-sandbox option. Native sessions use a separate launcher.
	args = append(args, "--dangerously-bypass-approvals-and-sandbox", "-")

	args, env, cleanup, err := sessionmcp.Configure(ctx, "codex", args, nil)
	if err != nil {
		return adapter.Result{}, err
	}
	defer cleanup()
	process, err := c.runner.Run(ctx, adapter.Command{
		Path:              c.binary,
		Args:              args,
		Env:               env,
		Dir:               request.Worktree,
		Stdin:             append(prompt, '\n'),
		Heartbeat:         request.Heartbeat,
		HeartbeatInterval: request.HeartbeatInterval,
		Liveness:          adapter.WorkerLiveness(request, ClassifyStreamLine),
	})
	if err != nil {
		return adapter.Result{}, fmt.Errorf("run Codex process: %w", err)
	}
	result := adapter.Result{
		Adapter:          "codex",
		AdapterVersion:   c.version,
		Model:            effectiveModel,
		ExitCode:         process.ExitCode,
		Stdout:           process.Stdout,
		Stderr:           process.Stderr,
		StartedAt:        process.StartedAt,
		EndedAt:          process.EndedAt,
		TimedOut:         process.TimedOut,
		Cancelled:        process.Cancelled,
		OutputTruncated:  process.OutputTruncated,
		Isolation:        process.Isolation,
		Status:           adapter.StatusSuccess,
		StoppedAfterDone: process.StoppedAfterDone,
		IdleTimedOut:     process.IdleTimedOut,
	}
	if process.StoppedAfterDone && !process.TimedOut && !process.Cancelled {
		// turn.completed was observed; the lingering process was stopped.
		result.ExitCode = 0
	}
	if result.ExitCode != 0 || process.TimedOut || process.Cancelled || process.IdleTimedOut {
		result.Status = adapter.StatusFailure
	} else if process.OutputTruncated {
		result.Status = adapter.StatusBlocked
	}
	parseJSONL(process.Stdout, &result)
	return result, nil
}

// ClassifyStreamLine maps one `codex exec --json` line to a liveness signal.
func ClassifyStreamLine(line []byte) adapter.StreamSignal {
	var event struct {
		Type string `json:"type"`
		Item struct {
			Type string `json:"type"`
		} `json:"item"`
	}
	if json.Unmarshal(line, &event) != nil {
		return adapter.SignalNone
	}
	tool := event.Item.Type == "command_execution" || event.Item.Type == "mcp_tool_call"
	switch {
	case event.Type == "turn.completed":
		return adapter.SignalDone
	case event.Type == "item.started" && tool:
		return adapter.SignalToolStart
	case event.Type == "item.completed" && tool:
		return adapter.SignalToolEnd
	}
	return adapter.SignalNone
}

func (c *Client) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusBlocked, fmt.Errorf("%w: Codex status is runtime-managed in V1", model.ErrUnavailable)
}

func (c *Client) Resume(context.Context, string, adapter.Request) (adapter.Result, error) {
	return adapter.Result{}, fmt.Errorf("%w: Codex resume is not exposed by Runtime V1", model.ErrUnavailable)
}

func (c *Client) Capabilities() map[string]string {
	return map[string]string{
		"run": "native", "sandbox": "native", "resume": "unsupported",
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
			"Prepare reviewable changes and report commands actually executed; the runtime creates the task commit.",
		},
	}
	payload, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Codex task prompt: %w", err)
	}
	return append([]byte("Execute this MARSHAL task envelope:\n"), payload...), nil
}

func parseJSONL(output []byte, result *adapter.Result) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		result.Events = append(result.Events, event)
		if eventType, _ := event["type"].(string); eventType == "thread.started" {
			if threadID, _ := event["thread_id"].(string); threadID != "" {
				result.SessionID = threadID
			} else if sid, _ := event["session_id"].(string); sid != "" {
				result.SessionID = sid
			}
		}
		if result.SessionID == "" {
			if threadID, _ := event["thread_id"].(string); threadID != "" {
				result.SessionID = threadID
			}
		}
		if item, ok := event["item"].(map[string]any); ok {
			if itemType, _ := item["type"].(string); itemType == "agent_message" {
				if text, _ := item["text"].(string); text != "" {
					result.FinalText = text
				}
			}
		}
	}
}
