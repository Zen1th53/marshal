package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

const (
	DefaultModel            = "gemini-3.8-flash-high"
	GovernedCLIVersion      = "1.2.14" // Pinned baseline qualification
	GovernedCLIMinVersion   = "1.2.14"
	GovernedCLIMaxVersion   = "1.4.2"
	GovernedCLIVersionRange = "1.2.14..1.4.2"
)

var safeObservedVersion = regexp.MustCompile(`^[0-9]{1,8}\.[0-9]{1,8}\.[0-9]{1,8}$`)

func parseSemverParts(v string) []int {
	var nums []int
	var cur int
	var inNum bool
	for i := 0; i < len(v); i++ {
		ch := v[i]
		if ch >= '0' && ch <= '9' {
			cur = cur*10 + int(ch-'0')
			inNum = true
		} else {
			if inNum {
				nums = append(nums, cur)
				cur = 0
				inNum = false
			}
		}
	}
	if inNum {
		nums = append(nums, cur)
	}
	return nums
}

func compareSemver(v1, v2 string) int {
	p1 := parseSemverParts(v1)
	p2 := parseSemverParts(v2)
	maxLen := len(p1)
	if len(p2) > maxLen {
		maxLen = len(p2)
	}
	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(p1) {
			n1 = p1[i]
		}
		if i < len(p2) {
			n2 = p2[i]
		}
		if n1 != n2 {
			if n1 < n2 {
				return -1
			}
			return 1
		}
	}
	if v1 < v2 {
		return -1
	} else if v1 > v2 {
		return 1
	}
	return 0
}

func isQualifiedGovernedVersion(v string) bool {
	if !safeObservedVersion.MatchString(v) {
		return false
	}
	if v == "1.2.14" {
		return true
	}
	// 1.3.x broke the single-JSON output format with multiple stream-JSON chunks in response
	if strings.HasPrefix(v, "1.3.") {
		return false
	}
	// 1.4.0 through 1.4.2 (validated against installed agy 1.4.2 contract)
	if compareSemver(v, "1.4.0") >= 0 && compareSemver(v, GovernedCLIMaxVersion) <= 0 {
		return true
	}
	return false
}

func isUnverifiedNewerVersion(v string) bool {
	if !safeObservedVersion.MatchString(v) {
		return false
	}
	return compareSemver(v, GovernedCLIMaxVersion) > 0
}

// Client wraps the native Antigravity (agy) print CLI. Registration as a Marshal
// governed driver and launch-scoped MCP configuration are separate capabilities.
type Client struct {
	binary              string
	runner              adapter.ProcessRunner
	version             string
	governed, probeOnly bool
	unverifiedNewer     bool
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

	c.version = ""
	// Lanes open in parallel; a short budget fails healthy probes under load.
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	versionRes, err := c.runner.Run(probeCtx, adapter.Command{
		Path: c.binary,
		Args: []string{"--version"},
	})
	if err != nil || versionRes.ExitCode != 0 || versionRes.TimedOut || versionRes.Cancelled || versionRes.OutputTruncated {
		if c.governed {
			return adapter.Probe{}, governedProbeFailure("version", versionRes, err)
		}
		return adapter.Probe{}, fmt.Errorf("%w: probe Antigravity version failed: %v", model.ErrUnavailable, err)
	}

	version := strings.TrimSpace(string(versionRes.Stdout))
	if version == "" {
		return adapter.Probe{}, fmt.Errorf("%w: Antigravity did not report a version", model.ErrUnavailable)
	}
	if err := c.validateGovernedVersion(version); err != nil {
		return adapter.Probe{}, err
	}
	helpCtx := probeCtx
	if c.governed {
		// Each confined startup gets its finite phase budget. The parent task
		// deadline still caps both phases, and no execution authority is added.
		cancel()
		var helpCancel context.CancelFunc
		helpCtx, helpCancel = context.WithTimeout(ctx, 30*time.Second)
		defer helpCancel()
	}
	helpRes, err := c.runner.Run(helpCtx, adapter.Command{Path: c.binary, Args: []string{"--help"}})
	if err != nil || helpRes.ExitCode != 0 || helpRes.TimedOut || helpRes.Cancelled || helpRes.OutputTruncated {
		if c.governed {
			return adapter.Probe{}, governedProbeFailure("help", helpRes, err)
		}
		return adapter.Probe{}, fmt.Errorf("%w: probe Antigravity help failed", model.ErrUnavailable)
	}
	help := string(helpRes.Stdout) + string(helpRes.Stderr)
	for _, flag := range []string{"--print", "--output-format", "--conversation", "--mode", "--sandbox", "--new-project", "--disable-slash-commands"} {
		if !strings.Contains(help, flag) {
			return adapter.Probe{}, fmt.Errorf("%w: Antigravity CLI lacks required flag %s", model.ErrUnavailable, flag)
		}
	}
	if c.governed && !strings.Contains(help, "--model") {
		return adapter.Probe{}, fmt.Errorf("%w: governed Antigravity requires explicit model selection", model.ErrUnavailable)
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

	args, err := commandArguments(request, prompt, "")
	if err != nil {
		return adapter.Result{}, err
	}
	ctx, args, env, cleanup, err := c.prepareInvocation(ctx, request, args, "")
	if err != nil {
		return adapter.Result{}, err
	}
	defer cleanup()

	process, err := c.runner.Run(ctx, adapter.Command{
		Path:              c.binary,
		Args:              args,
		Env:               env,
		Dir:               request.Worktree,
		Heartbeat:         request.Heartbeat,
		HeartbeatInterval: request.HeartbeatInterval,
	})
	if err != nil {
		return adapter.Result{}, fmt.Errorf("run Antigravity process: %w", err)
	}

	return c.processResult(process), nil
}

func (c *Client) Resume(ctx context.Context, sessionID string, request adapter.Request) (adapter.Result, error) {
	if c.runner == nil || c.binary == "" || sessionID == "" || request.TaskID == "" || request.Title == "" || request.Worktree == "" {
		return adapter.Result{}, fmt.Errorf("%w: incomplete Antigravity resume request", model.ErrInvalid)
	}

	prompt, err := buildPrompt(request)
	if err != nil {
		return adapter.Result{}, err
	}

	args, err := commandArguments(request, prompt, sessionID)
	if err != nil {
		return adapter.Result{}, err
	}
	ctx, args, env, cleanup, err := c.prepareInvocation(ctx, request, args, sessionID)
	if err != nil {
		return adapter.Result{}, err
	}
	defer cleanup()

	process, err := c.runner.Run(ctx, adapter.Command{
		Path:              c.binary,
		Args:              args,
		Env:               env,
		Dir:               request.Worktree,
		Heartbeat:         request.Heartbeat,
		HeartbeatInterval: request.HeartbeatInterval,
	})
	if err != nil {
		return adapter.Result{}, fmt.Errorf("resume Antigravity process: %w", err)
	}

	return c.processResult(process), nil
}

func (c *Client) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusBlocked, fmt.Errorf("%w: Antigravity status is runtime-managed", model.ErrUnavailable)
}

func (c *Client) Capabilities() map[string]string {
	// These are this adapter's implemented surfaces. The CLI's global MCP and
	// other features do not establish Marshal protocol or governed integration.
	capabilities := map[string]string{
		"instructions":              "native",
		"headless":                  "native",
		"structured_output":         "native",
		"mcp_client":                "unsupported",
		"native_server_or_protocol": "unsupported",
		"permissions":               "native",
		"sandbox":                   "native",
		"session_resume":            "native",
		"subagents":                 "unsupported",
		"artifacts":                 "unsupported",
		"hooks":                     "unsupported",
	}
	if c.governed && !c.probeOnly {
		capabilities["mcp_client"] = "native"
	}
	return capabilities
}

// GovernedPrompt renders the exact data envelope used by this adapter. It
// performs no provider call and conveys no execution or approval authority.
func GovernedPrompt(request adapter.Request) ([]byte, error) { return buildPrompt(request) }

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

// commandArguments supports the current native print CLI only. Plan mode and
// the CLI sandbox retain normal tool permission decisions; JSON prompt fields
// never grant permissions. This is not a registered Marshal governed driver.
func commandArguments(request adapter.Request, prompt []byte, sessionID string) ([]string, error) {
	modelName := request.Model
	if modelName == "" {
		modelName = DefaultModel
	}
	args := []string{"--mode", "plan", "--sandbox", "--disable-slash-commands", "--output-format", "json", "--model", modelName}
	if request.Effort != "" {
		switch request.Effort {
		case "low", "medium", "high", "max":
		default:
			return nil, fmt.Errorf("%w: invalid Antigravity effort", model.ErrInvalid)
		}
		args = append(args, "--effort", request.Effort)
	}
	if sessionID == "" {
		args = append(args, "--new-project")
	} else {
		args = append(args, "--conversation", sessionID)
	}
	return append(args, "--print", string(prompt)), nil
}

func (c *Client) processResult(process adapter.ProcessResult) adapter.Result {
	result := adapter.Result{
		Adapter: "antigravity", AdapterVersion: c.version, ExitCode: process.ExitCode,
		Stdout: process.Stdout, Stderr: process.Stderr, StartedAt: process.StartedAt,
		EndedAt: process.EndedAt, TimedOut: process.TimedOut, Cancelled: process.Cancelled,
		OutputTruncated: process.OutputTruncated, Isolation: process.Isolation,
		Status: adapter.StatusSuccess,
	}
	if process.ExitCode != 0 || process.TimedOut || process.Cancelled {
		result.Status = adapter.StatusFailure
	} else if process.OutputTruncated {
		result.Status = adapter.StatusBlocked
	}
	envelope := parseAntigravityOutput(process.Stdout, &result)
	if c.governed {
		result.NativeEnvelope = envelope
	}
	if c.governed {
		result.Adapter = "agy"
	}
	return result
}

// Parse only the final result envelope. Reasoning, thinking, internal steps and
// tool arguments are never treated as final text or normalized evidence events.
func parseAntigravityOutput(output []byte, result *adapter.Result) string {
	var response struct {
		ConversationID string            `json:"conversation_id"`
		Status         string            `json:"status"`
		Model          string            `json:"model"`
		Response       string            `json:"response"`
		IsError        bool              `json:"is_error"`
		DeniedActions  []json.RawMessage `json:"denied_actions"`
		Usage          struct {
			InputTokens  *int64   `json:"input_tokens"`
			OutputTokens *int64   `json:"output_tokens"`
			TotalTokens  *int64   `json:"total_tokens"`
			CostUSD      *float64 `json:"cost_usd"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		if result.Status == adapter.StatusSuccess {
			result.Status = adapter.StatusFailure
		}
		return "invalid_json"
	}
	result.SessionID = response.ConversationID
	result.FinalText = response.Response
	result.Model = response.Model
	result.Usage = adapter.Usage{
		Reported:     response.Usage.InputTokens != nil || response.Usage.OutputTokens != nil || response.Usage.TotalTokens != nil || response.Usage.CostUSD != nil,
		PromptTokens: response.Usage.InputTokens, CompletionTokens: response.Usage.OutputTokens,
		TotalTokens: response.Usage.TotalTokens, CostUSD: response.Usage.CostUSD,
	}
	envelope := "success"
	switch {
	case response.IsError:
		envelope = "is_error"
	case response.Status == "":
		envelope = "missing_status"
	case strings.ToUpper(response.Status) != "SUCCESS":
		envelope = "non_success_status"
	case len(response.DeniedActions) > 0:
		envelope = "denied_actions"
	case strings.TrimSpace(response.Response) == "":
		envelope = "empty_response"
	}
	if result.Status != adapter.StatusSuccess {
		return envelope
	}
	if response.IsError || strings.ToUpper(response.Status) != "SUCCESS" {
		result.Status = adapter.StatusFailure
	} else if len(response.DeniedActions) > 0 || strings.TrimSpace(response.Response) == "" {
		result.Status = adapter.StatusBlocked
	}
	return envelope
}

// CollectEvidence reports metadata already observed in the result. It never
// queries a session, imports raw output or supplies missing provider facts.
func (c *Client) CollectEvidence(result adapter.Result) map[string]any {
	metadata := map[string]any{"adapter": result.Adapter, "adapter_version": result.AdapterVersion, "session_id": result.SessionID, "exit_code": result.ExitCode, "started_at": result.StartedAt, "ended_at": result.EndedAt, "timed_out": result.TimedOut, "output_truncated": result.OutputTruncated}
	if c.governed || result.Cancelled {
		metadata["cancelled"] = result.Cancelled
	}
	if c.governed && nativeOutcomeMetadataValid(result) {
		outcome := map[string]any{"status": string(result.Status), "exit_code": result.ExitCode, "timed_out": result.TimedOut, "cancelled": result.Cancelled, "output_truncated": result.OutputTruncated, "native_envelope": result.NativeEnvelope}
		if c.unverifiedNewer {
			outcome["execution_mode"] = "native"
		}
		metadata["process_outcome"] = outcome
	}
	if c.unverifiedNewer {
		metadata["unverified_version"] = true
	}
	return metadata
}

// Runtime owns cancellation and process-group lifecycle for native print.
func (c *Client) Shutdown(context.Context, string) error {
	return fmt.Errorf("%w: Antigravity shutdown is runtime-managed", model.ErrUnavailable)
}

// Private governed failures retain finite process metadata, never native output
// or arbitrary runner error text. Community Probe keeps its existing errors.
func governedProbeFailure(phase string, result adapter.ProcessResult, err error) error {
	if phase != "version" && phase != "help" {
		phase = "unknown"
	}
	runner := "none"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		runner = "deadline"
	case errors.Is(err, context.Canceled):
		runner = "cancelled"
	case err != nil:
		runner = "error"
	}
	exit := "unknown"
	if result.ExitCode >= -1 && result.ExitCode <= 255 {
		exit = fmt.Sprint(result.ExitCode)
	}
	return fmt.Errorf("%w: probe Antigravity %s failed (runner=%s; exit=%s; timed_out=%t; cancelled=%t; output_truncated=%t)", model.ErrUnavailable, phase, runner, exit, result.TimedOut, result.Cancelled, result.OutputTruncated)
}

// Reject caller-supplied lookalikes before metadata publication. Only the
// pinned parser's finite vocabulary and consistent process facts are exposed.
func nativeOutcomeMetadataValid(r adapter.Result) bool {
	if r.ExitCode < -1 || r.ExitCode > 255 || (r.TimedOut && r.Cancelled) {
		return false
	}
	switch r.NativeEnvelope {
	case "success", "invalid_json", "is_error", "missing_status", "non_success_status", "denied_actions", "empty_response":
	default:
		return false
	}
	expected := adapter.StatusSuccess
	if r.ExitCode != 0 || r.TimedOut || r.Cancelled {
		expected = adapter.StatusFailure
	} else if r.OutputTruncated {
		expected = adapter.StatusBlocked
	} else {
		switch r.NativeEnvelope {
		case "invalid_json", "is_error", "missing_status", "non_success_status":
			expected = adapter.StatusFailure
		case "denied_actions", "empty_response":
			expected = adapter.StatusBlocked
		}
	}
	return r.Status == expected
}
