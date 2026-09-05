package antigravity

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

const (
	DefaultModel = "gemini-3.8-flash"
)

// Client wraps the native Google Antigravity (agy) CLI as a first-class MARSHAL harness.
type Client struct {
	binary  string
	runner  adapter.ProcessRunner
	version string
}

func New(binary string, runner adapter.ProcessRunner) *Client {
	if binary == "" {
		binary = "agy"
	}
	return &Client{binary: binary, runner: runner}
}

func (c *Client) Probe(ctx context.Context) (adapter.Probe, error) {
	if c.binary == "" || c.runner == nil {
		return adapter.Probe{}, fmt.Errorf("%w: Antigravity binary and process runner are required", model.ErrInvalid)
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	versionRes, err := c.runner.Run(probeCtx, adapter.Command{
		Path: c.binary,
		Args: []string{"--version"},
	})
	if err != nil || versionRes.ExitCode != 0 {
		return adapter.Probe{}, fmt.Errorf("%w: probe Antigravity version failed: %v", model.ErrUnavailable, err)
	}

	version := strings.TrimSpace(string(versionRes.Stdout))
	if version == "" {
		version = "2.1.0"
	}
	c.version = version

	return adapter.Probe{
		Name:         "antigravity",
		Available:    true,
		Version:      c.version,
		Capabilities: c.Capabilities(),
	}, nil
}

func (c *Client) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	if c.runner == nil || c.binary == "" || request.TaskID == "" || request.Title == "" || request.Worktree == "" {
		return adapter.Result{}, fmt.Errorf("%w: incomplete Antigravity run request", model.ErrInvalid)
	}

	prompt, err := buildPrompt(request)
	if err != nil {
		return adapter.Result{}, err
	}

	modelName := request.Model
	if modelName == "" {
		modelName = DefaultModel
	}

	args := []string{
		"exec",
		"--json",
		"--model", modelName,
		"--cd", request.Worktree,
		"--prompt", string(prompt),
	}

	process, err := c.runner.Run(ctx, adapter.Command{
		Path:              c.binary,
		Args:              args,
		Dir:               request.Worktree,
		Heartbeat:         request.Heartbeat,
		HeartbeatInterval: request.HeartbeatInterval,
	})
	if err != nil {
		return adapter.Result{}, fmt.Errorf("run Antigravity process: %w", err)
	}

	result := adapter.Result{
		Adapter:         "antigravity",
		AdapterVersion:  c.version,
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

	parseAntigravityOutput(process.Stdout, &result)
	return result, nil
}

func (c *Client) Resume(ctx context.Context, sessionID string, request adapter.Request) (adapter.Result, error) {
	if c.runner == nil || c.binary == "" || sessionID == "" || request.Worktree == "" {
		return adapter.Result{}, fmt.Errorf("%w: incomplete Antigravity resume request", model.ErrInvalid)
	}

	prompt, err := buildPrompt(request)
	if err != nil {
		return adapter.Result{}, err
	}

	args := []string{
		"exec",
		"--json",
		"--resume", sessionID,
		"--cd", request.Worktree,
		"--prompt", string(prompt),
	}

	process, err := c.runner.Run(ctx, adapter.Command{
		Path:              c.binary,
		Args:              args,
		Dir:               request.Worktree,
		Heartbeat:         request.Heartbeat,
		HeartbeatInterval: request.HeartbeatInterval,
	})
	if err != nil {
		return adapter.Result{}, fmt.Errorf("resume Antigravity process: %w", err)
	}

	result := adapter.Result{
		Adapter:         "antigravity",
		AdapterVersion:  c.version,
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
	}

	parseAntigravityOutput(process.Stdout, &result)
	return result, nil
}

func (c *Client) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusBlocked, fmt.Errorf("%w: Antigravity status is runtime-managed", model.ErrUnavailable)
}

func (c *Client) Capabilities() map[string]string {
	return map[string]string{
		"instructions":              "native",
		"headless":                  "native",
		"structured_output":         "native",
		"mcp_client":                "native",
		"native_server_or_protocol": "native",
		"permissions":               "native",
		"sandbox":                   "native",
		"session_resume":            "native",
		"subagents":                 "native",
		"artifacts":                 "native",
		"hooks":                     "native",
	}
}

func buildPrompt(req adapter.Request) ([]byte, error) {
	payload := map[string]any{
		"task_id":            req.TaskID,
		"title":              req.Title,
		"allowed_operations": req.AllowedOperations,
		"evidence_required":  req.EvidenceRequired,
		"trusted_context":    req.TrustedContext,
	}
	return json.Marshal(payload)
}

func parseAntigravityOutput(output []byte, result *adapter.Result) {
	if len(output) == 0 {
		return
	}

	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}

		var event struct {
			Type   string `json:"type"`
			Tokens struct {
				Total      *int64   `json:"total"`
				Prompt     *int64   `json:"prompt"`
				Completion *int64   `json:"completion"`
				CostUSD    *float64 `json:"cost_usd"`
			} `json:"tokens"`
			Usage struct {
				TotalTokens      *int64   `json:"total_tokens"`
				PromptTokens     *int64   `json:"prompt_tokens"`
				CompletionTokens *int64   `json:"completion_tokens"`
				CostUSD          *float64 `json:"cost_usd"`
			} `json:"usage"`
			SessionID string `json:"session_id"`
		}

		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}

		// Extract usage preserving nil values when not reported
		if event.Tokens.Total != nil || event.Usage.TotalTokens != nil {
			total := event.Tokens.Total
			if total == nil {
				total = event.Usage.TotalTokens
			}
			prompt := event.Tokens.Prompt
			if prompt == nil {
				prompt = event.Usage.PromptTokens
			}
			comp := event.Tokens.Completion
			if comp == nil {
				comp = event.Usage.CompletionTokens
			}
			cost := event.Tokens.CostUSD
			if cost == nil {
				cost = event.Usage.CostUSD
			}

			result.Usage = adapter.Usage{
				Reported:         true,
				TotalTokens:      total,
				PromptTokens:     prompt,
				CompletionTokens: comp,
				CostUSD:          cost,
			}
		}
	}
}
