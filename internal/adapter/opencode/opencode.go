package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

type Client struct {
	binary  string
	runner  adapter.ProcessRunner
	version string
	model   string
}

func New(binary string, runner adapter.ProcessRunner) *Client {
	return &Client{binary: binary, runner: runner}
}

func NewWithModel(binary string, runner adapter.ProcessRunner, model string) *Client {
	return &Client{binary: binary, runner: runner, model: model}
}

func (c *Client) SetModel(model string) {
	c.model = model
}

func (c *Client) Probe(ctx context.Context) (adapter.Probe, error) {
	if c.binary == "" || c.runner == nil {
		return adapter.Probe{}, fmt.Errorf("%w: OpenCode binary and process runner are required", model.ErrInvalid)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	versionRes, err := c.runner.Run(probeCtx, adapter.Command{
		Path: c.binary, Args: []string{"--version"},
	})
	if err != nil || versionRes.ExitCode != 0 {
		return adapter.Probe{}, fmt.Errorf("%w: probe OpenCode version failed: %v", model.ErrUnavailable, err)
	}
	c.version = strings.TrimSpace(string(versionRes.Stdout))

	return adapter.Probe{
		Name: "opencode", Available: true, Version: c.version, Capabilities: c.Capabilities(),
	}, nil
}

func (c *Client) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	if c.runner == nil || c.binary == "" || request.TaskID == "" || request.Title == "" || request.Worktree == "" {
		return adapter.Result{}, fmt.Errorf("%w: incomplete OpenCode run request", model.ErrInvalid)
	}
	prompt, err := buildPrompt(request)
	if err != nil {
		return adapter.Result{}, err
	}

	modelOpt := c.model
	if modelOpt == "" {
		modelOpt = strings.TrimSpace(os.Getenv("MARSHAL_OPENCODE_MODEL"))
	}

	args := []string{"run", "--format", "json"}
	if modelOpt != "" {
		args = append(args, "-m", modelOpt)
	}
	args = append(args, string(prompt))

	process, err := c.runner.Run(ctx, adapter.Command{
		Path:              c.binary,
		Args:              args,
		Dir:               request.Worktree,
		Heartbeat:         request.Heartbeat,
		HeartbeatInterval: request.HeartbeatInterval,
	})
	if err != nil {
		return adapter.Result{}, fmt.Errorf("run OpenCode process: %w", err)
	}
	result := adapter.Result{
		Adapter: "opencode", AdapterVersion: c.version, ExitCode: process.ExitCode,
		Stdout: process.Stdout, Stderr: process.Stderr, StartedAt: process.StartedAt,
		EndedAt: process.EndedAt, TimedOut: process.TimedOut,
		Cancelled: process.Cancelled, OutputTruncated: process.OutputTruncated,
		Isolation: process.Isolation,
		Status:    adapter.StatusSuccess,
	}
	if process.ExitCode != 0 || process.TimedOut || process.Cancelled {
		result.Status = adapter.StatusFailure
	} else if process.OutputTruncated {
		result.Status = adapter.StatusBlocked
	}
	parseOpenCodeOutput(process.Stdout, &result)
	return result, nil
}

func (c *Client) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusBlocked, fmt.Errorf("%w: OpenCode status is runtime-managed", model.ErrUnavailable)
}

func (c *Client) Resume(context.Context, string, adapter.Request) (adapter.Result, error) {
	return adapter.Result{}, fmt.Errorf("%w: OpenCode resume is not exposed by Runtime", model.ErrUnavailable)
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
		return nil, fmt.Errorf("encode OpenCode task prompt: %w", err)
	}
	return append([]byte("Execute this MARSHAL task envelope:\n"), payload...), nil
}

func parseOpenCodeOutput(output []byte, result *adapter.Result) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		result.Events = append(result.Events, event)
		if sessID, ok := event["session_id"].(string); ok && sessID != "" {
			result.SessionID = sessID
		}
		if resText, ok := event["result"].(string); ok && resText != "" {
			result.FinalText = resText
		}
	}
	if result.FinalText == "" && len(output) > 0 {
		result.FinalText = string(output)
	}
}
