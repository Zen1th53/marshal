package execution

import (
	"fmt"
	"math"
	"time"
)

// ProviderObservation retains runtime observations, not billing or inference
// intervals. A process interval includes setup, tools and provider waits. A
// native turn lifecycle also includes any approval pauses. Unknown clocks and
// usage remain absent; they must never be derived from dispatch timestamps.
type ProviderObservation struct {
	Provider        string `json:"provider"`
	Model           string `json:"model,omitempty"`
	RunID           string `json:"run_id"`
	TaskID          string `json:"task_id"`
	CanonicalTaskID string `json:"canonical_task_id,omitempty"`
	// Attempt counts execution-engine task entries, including approval waits.
	// It is separate from the canonical approval and Marshal hand-in attempt.
	Attempt int `json:"attempt"`
	// Empty preserves legacy unknown semantics. This basis does not count
	// provider invocations, model calls or billing turns.
	AttemptBasis   string                  `json:"attempt_basis,omitempty"`
	SessionID      string                  `json:"session_id,omitempty"`
	ThreadID       string                  `json:"thread_id,omitempty"`
	TurnID         string                  `json:"turn_id,omitempty"`
	TimingBasis    string                  `json:"timing_basis"`
	StartedAt      *time.Time              `json:"started_at,omitempty"`
	EndedAt        *time.Time              `json:"ended_at,omitempty"`
	Usage          ProviderUsage           `json:"usage"`
	CLIEvents      *ProviderCLIEvents      `json:"cli_events,omitempty"`
	ProcessOutcome *ProviderProcessOutcome `json:"process_outcome,omitempty"`
}

// ProviderProcessOutcome preserves finite native process/parser facts only.
// It does not establish task success, a model call, billing or cause text.
// Nil means unavailable, including legacy and other-provider observations.
type ProviderProcessOutcome struct {
	Status          string `json:"status"`
	ExitCode        int    `json:"exit_code"`
	TimedOut        bool   `json:"timed_out"`
	Cancelled       bool   `json:"cancelled"`
	OutputTruncated bool   `json:"output_truncated"`
	NativeEnvelope  string `json:"native_envelope"`
}

func validateProviderProcessOutcome(p *ProviderProcessOutcome, provider string) error {
	if p == nil {
		return nil
	}
	if provider != "agy" || p.ExitCode < -1 || p.ExitCode > 255 || (p.TimedOut && p.Cancelled) {
		return fmt.Errorf("%w: invalid native process outcome", ErrRunInvalid)
	}
	switch p.NativeEnvelope {
	case "success", "invalid_json", "is_error", "missing_status", "non_success_status", "denied_actions", "empty_response":
	default:
		return fmt.Errorf("%w: unknown native envelope outcome", ErrRunInvalid)
	}
	expected := "success"
	if p.ExitCode != 0 || p.TimedOut || p.Cancelled {
		expected = "failure"
	} else if p.OutputTruncated {
		expected = "blocked"
	} else {
		switch p.NativeEnvelope {
		case "invalid_json", "is_error", "missing_status", "non_success_status":
			expected = "failure"
		case "denied_actions", "empty_response":
			expected = "blocked"
		}
	}
	if p.Status != expected {
		return fmt.Errorf("%w: native outcome status contradicts process/parser facts", ErrRunInvalid)
	}
	return nil
}

// ProviderCLIEvents counts only retained native CLI event emissions. It does
// not prove host tool effects, permission grants, model calls or billing.
// Nil means unavailable. Permission events stay nil when the CLI does not emit them.
type ProviderCLIEvents struct {
	Basis            string                       `json:"basis"`
	CapturedEvents   int                          `json:"captured_events"`
	ToolUseEvents    int                          `json:"tool_use_events"`
	ToolsCompleted   int                          `json:"tools_completed"`
	ToolsFailed      int                          `json:"tools_failed"`
	PermissionEvents *ProviderCLIPermissionEvents `json:"permission_events,omitempty"`
}
type ProviderCLIPermissionEvents struct {
	Asked    int `json:"asked"`
	Rejected int `json:"rejected"`
}

// ProviderUsage preserves the provider's explicit report/unknown distinction.
type ProviderUsage struct {
	Reported         bool     `json:"reported"`
	PromptTokens     *int64   `json:"prompt_tokens,omitempty"`
	CompletionTokens *int64   `json:"completion_tokens,omitempty"`
	TotalTokens      *int64   `json:"total_tokens,omitempty"`
	CostUSD          *float64 `json:"cost_usd,omitempty"`
	ModelCalls       int      `json:"model_calls,omitempty"`
	DurationMs       int64    `json:"duration_ms,omitempty"`
}

// ValidateProviderObservation refuses unbounded or malformed metadata before
// it crosses the evidence/hand-in boundary. Nil permits old persisted records.
func ValidateProviderObservation(o *ProviderObservation) error {
	if o == nil {
		return nil
	}
	for _, id := range []string{o.Provider, o.Model, o.RunID, o.TaskID, o.CanonicalTaskID, o.SessionID, o.ThreadID, o.TurnID} {
		if len(id) > 256 {
			return fmt.Errorf("%w: provider observation identifier too long", ErrRunInvalid)
		}
		for _, c := range id {
			if c < 0x21 || c > 0x7e {
				return fmt.Errorf("%w: provider observation identifier malformed", ErrRunInvalid)
			}
		}
	}
	if err := validateProviderProcessOutcome(o.ProcessOutcome, o.Provider); err != nil {
		return err
	}
	switch o.TimingBasis {
	case "adapter_process", "runtime_native_turn_lifecycle", "unknown":
	default:
		return fmt.Errorf("%w: unknown provider observation timing basis", ErrRunInvalid)
	}
	if (o.StartedAt == nil) != (o.EndedAt == nil) {
		return fmt.Errorf("%w: incomplete provider observation interval", ErrRunInvalid)
	}
	if o.StartedAt != nil && (o.StartedAt.IsZero() || o.EndedAt.IsZero() || o.EndedAt.Before(*o.StartedAt) || o.TimingBasis == "unknown") {
		return fmt.Errorf("%w: invalid provider observation interval", ErrRunInvalid)
	}
	if o.Attempt < 0 || o.Usage.ModelCalls < 0 || o.Usage.DurationMs < 0 {
		return fmt.Errorf("%w: negative provider observation usage", ErrRunInvalid)
	}
	if c := o.CLIEvents; c != nil {
		if c.Basis != "native_cli_event" || c.CapturedEvents < 1 || c.CapturedEvents > 4096 || c.ToolUseEvents < 0 || c.ToolsCompleted < 0 || c.ToolsFailed < 0 || c.ToolUseEvents != c.ToolsCompleted+c.ToolsFailed || c.ToolUseEvents > c.CapturedEvents || c.ToolsCompleted > c.CapturedEvents || c.ToolsFailed > c.CapturedEvents {
			return fmt.Errorf("%w: invalid native CLI event counts", ErrRunInvalid)
		}
		if p := c.PermissionEvents; p != nil && (p.Asked < 0 || p.Rejected < 0 || p.Asked > c.CapturedEvents || p.Rejected > c.CapturedEvents || p.Asked+p.Rejected > c.CapturedEvents) {
			return fmt.Errorf("%w: invalid native permission event counts", ErrRunInvalid)
		}
	}
	if o.AttemptBasis != "" && o.AttemptBasis != "execution_engine_admission" {
		return fmt.Errorf("%w: unknown provider observation attempt basis", ErrRunInvalid)
	}
	if !o.Usage.Reported && (o.Usage.PromptTokens != nil || o.Usage.CompletionTokens != nil || o.Usage.TotalTokens != nil || o.Usage.CostUSD != nil) {
		return fmt.Errorf("%w: unreported provider observation resources", ErrRunInvalid)
	}
	if o.Usage.CostUSD != nil && (*o.Usage.CostUSD < 0 || math.IsNaN(*o.Usage.CostUSD) || math.IsInf(*o.Usage.CostUSD, 0)) {
		return fmt.Errorf("%w: invalid provider observation cost", ErrRunInvalid)
	}
	for _, n := range []*int64{o.Usage.PromptTokens, o.Usage.CompletionTokens, o.Usage.TotalTokens} {
		if n != nil && *n < 0 {
			return fmt.Errorf("%w: negative provider observation tokens", ErrRunInvalid)
		}
	}
	return nil
}

// cloneProviderObservation gives evidence owners independent snapshots of all
// optional clocks and resource values. Validation must apply to that owned copy.
func cloneProviderObservation(o *ProviderObservation) *ProviderObservation {
	if o == nil {
		return nil
	}
	copied := *o
	copied.StartedAt = cloneObservedValue(o.StartedAt)
	copied.EndedAt = cloneObservedValue(o.EndedAt)
	copied.Usage.PromptTokens = cloneObservedValue(o.Usage.PromptTokens)
	copied.Usage.CompletionTokens = cloneObservedValue(o.Usage.CompletionTokens)
	copied.Usage.TotalTokens = cloneObservedValue(o.Usage.TotalTokens)
	copied.Usage.CostUSD = cloneObservedValue(o.Usage.CostUSD)
	copied.CLIEvents = cloneObservedValue(o.CLIEvents)
	copied.ProcessOutcome = cloneObservedValue(o.ProcessOutcome)
	if copied.CLIEvents != nil {
		copied.CLIEvents.PermissionEvents = cloneObservedValue(o.CLIEvents.PermissionEvents)
	}
	return &copied
}
func cloneObservedValue[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
