//go:build !governed

package app

import (
	"encoding/json"
	"fmt"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func process05ProviderObservation(task execution.TaskExecution, result adapter.Result, native *execution.NativeTurnBinding, basis string) *execution.ProviderObservation {
	o := &execution.ProviderObservation{Provider: result.Adapter, Model: result.Model, RunID: task.RunID, TaskID: task.TaskID, CanonicalTaskID: task.CanonicalTaskID, Attempt: task.Attempts, AttemptBasis: "execution_engine_admission", SessionID: result.SessionID, TimingBasis: basis, CLIEvents: process05CLIEventCounts(result),
		Usage: execution.ProviderUsage{Reported: result.Usage.Reported, PromptTokens: copyProviderValue(result.Usage.PromptTokens), CompletionTokens: copyProviderValue(result.Usage.CompletionTokens), TotalTokens: copyProviderValue(result.Usage.TotalTokens), CostUSD: copyProviderValue(result.Usage.CostUSD), ModelCalls: result.Usage.ModelCalls, DurationMs: result.Usage.DurationMs}}
	if result.Adapter == "agy" && native == nil && result.NativeEnvelope != "" {
		o.ProcessOutcome = &execution.ProviderProcessOutcome{Status: string(result.Status), ExitCode: result.ExitCode, TimedOut: result.TimedOut, Cancelled: result.Cancelled, OutputTruncated: result.OutputTruncated, NativeEnvelope: result.NativeEnvelope}
	}
	if native != nil {
		o.Provider = native.Provider
		o.ThreadID = native.ThreadID
		o.TurnID = native.TurnID
		o.SessionID = native.SessionID
		o.Model = native.Model
	}
	if !result.StartedAt.IsZero() && !result.EndedAt.IsZero() && !result.EndedAt.Before(result.StartedAt) {
		start, end := result.StartedAt.UTC(), result.EndedAt.UTC()
		o.StartedAt = &start
		o.EndedAt = &end
	} else {
		o.TimingBasis = "unknown"
	}
	return o
}

func copyProviderValue[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// marshalProcess05Observations projects only bounded typed retained metadata,
// never raw provider output. It uses durable run evidence so restart collection
// does not depend on the in-memory oracle.
func marshalProcess05Observations(run *execution.ExecutionRun, task execution.TaskExecution) ([]marshal.CommandRecord, error) {
	var records []marshal.CommandRecord
	total := 0
	for _, ev := range run.Evidence {
		o := ev.ProviderObservation
		if ev.TaskID != task.TaskID || o == nil {
			continue
		}
		if ev.Status != execution.EvidenceValid || o.RunID != run.RunID || o.TaskID != task.TaskID || o.CanonicalTaskID != task.CanonicalTaskID || o.Attempt != task.Attempts {
			return nil, fmt.Errorf("process 05: provider observation binding differs from completed task")
		}
		if err := execution.ValidateProviderObservation(o); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(o)
		if err != nil {
			return nil, err
		}
		total += len(encoded)
		if len(encoded) > 4096 || total > 16384 || len(records) >= 16 {
			return nil, fmt.Errorf("process 05: provider observations exceed hand-in bound")
		}
		records = append(records, marshal.CommandRecord{Command: providerObservationRecordPrefix + ev.EvidenceID, Output: string(encoded) + providerObservationRecordSuffix})
	}
	return records, nil
}

// The pinned OpenCode JSON stream emits completed/error tool_use envelopes.
// Permission warnings are outside that JSON stream, so their absence never
// becomes a count of zero permission requests. No native payload is retained.
func process05CLIEventCounts(result adapter.Result) *execution.ProviderCLIEvents {
	if result.Adapter != "opencode" || result.SessionID == "" || len(result.Events) == 0 || len(result.Events) > 4096 || result.OutputTruncated {
		return nil
	}
	counts := &execution.ProviderCLIEvents{Basis: "native_cli_event"}
	for _, e := range result.Events {
		session, ok := e["sessionID"].(string)
		if !ok || session != result.SessionID {
			return nil
		}
		timestamp, ok := e["timestamp"].(float64)
		if !ok || timestamp <= 0 || timestamp > 9007199254740991 || timestamp != float64(int64(timestamp)) {
			return nil
		}
		kind, ok := e["type"].(string)
		if !ok {
			return nil
		}
		part, _ := e["part"].(map[string]any)
		switch kind {
		case "tool_use":
			if part["type"] != "tool" {
				return nil
			}
			state, ok := part["state"].(map[string]any)
			if !ok {
				return nil
			}
			switch state["status"] {
			case "completed":
				counts.ToolsCompleted++
			case "error":
				counts.ToolsFailed++
			default:
				return nil
			}
			counts.ToolUseEvents++
		case "text":
			if part["type"] != "text" {
				return nil
			}
		case "step_start":
			if part["type"] != "step-start" {
				return nil
			}
		case "step_finish":
			if part["type"] != "step-finish" {
				return nil
			}
		case "reasoning":
			if part["type"] != "reasoning" {
				return nil
			}
		case "error":
			if _, ok := e["error"].(map[string]any); !ok {
				return nil
			}
		default:
			return nil
		}
		counts.CapturedEvents++
	}
	return counts
}
