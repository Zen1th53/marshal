package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

type Client struct {
	binary  string
	runner  adapter.ProcessRunner
	version string
}

func New(binary string, runner adapter.ProcessRunner) *Client {
	return &Client{binary: binary, runner: runner}
}

func (c *Client) Probe(ctx context.Context) (adapter.Probe, error) {
	if c.binary == "" || c.runner == nil {
		return adapter.Probe{}, fmt.Errorf("%w: Codex binary and process runner are required", model.ErrInvalid)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, err := commandOutput(probeCtx, c.binary, "--version")
	if err != nil {
		return adapter.Probe{}, fmt.Errorf("%w: probe Codex version: %v", model.ErrUnavailable, err)
	}
	help, err := commandOutput(probeCtx, c.binary, "exec", "--help")
	if err != nil {
		return adapter.Probe{}, fmt.Errorf("%w: probe Codex exec: %v", model.ErrUnavailable, err)
	}
	for _, required := range []string{"--json", "--sandbox", "--ephemeral", "--ignore-user-config", "--cd"} {
		if !strings.Contains(help, required) {
			return adapter.Probe{}, fmt.Errorf("%w: Codex exec lacks required flag %s", model.ErrUnavailable, required)
		}
	}
	c.version = strings.TrimSpace(version)
	return adapter.Probe{
		Name: "codex", Available: true, Version: c.version, Capabilities: c.Capabilities(),
	}, nil
}

func (c *Client) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	if c.runner == nil || c.binary == "" || request.TaskID == "" ||
		request.Title == "" || request.Worktree == "" {
		return adapter.Result{}, fmt.Errorf("%w: incomplete Codex run request", model.ErrInvalid)
	}
	prompt, err := buildPrompt(request)
	if err != nil {
		return adapter.Result{}, err
	}
	process, err := c.runner.Run(ctx, adapter.Command{
		Path: c.binary,
		Args: []string{
			"exec", "--json", "-C", request.Worktree, "-s", "workspace-write",
			"--ephemeral", "--ignore-user-config", "-",
		},
		Dir:               request.Worktree,
		Stdin:             append(prompt, '\n'),
		Heartbeat:         request.Heartbeat,
		HeartbeatInterval: request.HeartbeatInterval,
	})
	if err != nil {
		return adapter.Result{}, fmt.Errorf("run Codex process: %w", err)
	}
	result := adapter.Result{
		Adapter: "codex", AdapterVersion: c.version, ExitCode: process.ExitCode,
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

func commandOutput(ctx context.Context, binary string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return string(output), nil
}
