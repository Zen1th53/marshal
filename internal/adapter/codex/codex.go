package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
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
	if c.binary == "" || c.runner == nil {
		return adapter.Probe{}, fmt.Errorf("%w: Codex binary and process runner are required", model.ErrInvalid)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	versionResult, err := c.runner.Run(probeCtx, adapter.Command{Path: c.binary, Args: []string{"--version"}})
	if err != nil || versionResult.ExitCode != 0 {
		return adapter.Probe{}, fmt.Errorf("%w: probe Codex version failed: %v", model.ErrUnavailable, err)
	}
	helpResult, err := c.runner.Run(probeCtx, adapter.Command{Path: c.binary, Args: []string{"exec", "--help"}})
	if err != nil || helpResult.ExitCode != 0 {
		return adapter.Probe{}, fmt.Errorf("%w: probe Codex exec failed: %v", model.ErrUnavailable, err)
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
		if models, def, mErr := DiscoverModels(probeCtx, c.binary, c.runner); mErr == nil && len(models) > 0 {
			c.models = models
			c.defaultModel = def
		}
	}
	return adapter.Probe{
		Name: "codex", Available: true, Version: c.version, Capabilities: c.Capabilities(),
	}, nil
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

	prompt, err := buildPrompt(request)
	if err != nil {
		return adapter.Result{}, err
	}
	args := []string{
		"exec", "--json", "-C", request.Worktree, "-s", "workspace-write",
		"--ephemeral", "--ignore-user-config",
	}
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	args = append(args, "-")
	if err := ValidateDangerousFlags(args); err != nil {
		return adapter.Result{}, err
	}

	process, err := c.runner.Run(ctx, adapter.Command{
		Path:              c.binary,
		Args:              args,
		Dir:               request.Worktree,
		Stdin:             append(prompt, '\n'),
		Heartbeat:         request.Heartbeat,
		HeartbeatInterval: request.HeartbeatInterval,
	})
	if err != nil {
		return adapter.Result{}, fmt.Errorf("run Codex process: %w", err)
	}
	result := adapter.Result{
		Adapter:         "codex",
		AdapterVersion:  c.version,
		Model:           effectiveModel,
		ExitCode:        process.ExitCode,
		Stdout:          process.Stdout,
		Stderr:          process.Stderr,
		StartedAt:       process.StartedAt,
		EndedAt:         process.EndedAt,
		TimedOut:        process.TimedOut,
		Cancelled:       process.Cancelled,
		OutputTruncated: process.OutputTruncated,
		Isolation:       process.Isolation,
		Status:          adapter.StatusSuccess,
	}
	if process.ExitCode != 0 || process.TimedOut || process.Cancelled {
		result.Status = adapter.StatusFailure
	} else if process.OutputTruncated {
		result.Status = adapter.StatusBlocked
	}
	parseJSONL(process.Stdout, &result)
	return result, nil
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
