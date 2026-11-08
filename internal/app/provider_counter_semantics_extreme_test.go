//go:build !governed

package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
)

func TestPrivateProviderCounterMeaningIsVisibleToReviewers(t *testing.T) {
	task := execution.TaskExecution{RunID: "lane", TaskID: "task", CanonicalTaskID: "canonical", Attempts: 2}
	o := process05ProviderObservation(task, adapter.Result{Adapter: "codex", Model: "known-native"}, nil, "adapter_process")
	raw, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var shown map[string]any
	if err := json.Unmarshal(raw, &shown); err != nil {
		t.Fatal(err)
	}
	if shown["attempt_basis"] != "execution_engine_admission" || shown["attempt"] != float64(2) {
		t.Fatalf("reviewer cannot distinguish admission entries from actual provider calls: %s", raw)
	}
	if _, invented := shown["actual_call_count"]; invented {
		t.Fatal("admission counter became actual call count")
	}
}

func TestPrivateCLIEventCountsPreserveUnknownAndOnlyTypedEmission(t *testing.T) {
	event := func(kind, status string) map[string]any {
		part := map[string]any{"type": "text", "text": "not retained"}
		if kind == "tool_use" {
			part = map[string]any{"type": "tool", "tool": "sensitive-path-and-arguments", "state": map[string]any{"status": status, "error": "sensitive-error"}}
		}
		return map[string]any{"type": kind, "timestamp": float64(1234), "sessionID": "native-session", "part": part}
	}
	for _, kind := range []string{"complete-error", "text-only", "missing", "forged-text", "foreign-session", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			result := adapter.Result{Adapter: "opencode", SessionID: "native-session"}
			switch kind {
			case "complete-error":
				result.Events = []map[string]any{event("tool_use", "completed"), event("tool_use", "error"), event("text", "")}
			case "text-only":
				result.Events = []map[string]any{event("text", "")}
			case "forged-text":
				result.FinalText = `{"type":"tool_use","state":"completed"}`
			case "foreign-session":
				e := event("tool_use", "completed")
				e["sessionID"] = "foreign"
				result.Events = []map[string]any{e}
			case "truncated":
				result.Events = []map[string]any{event("text", "")}
				result.OutputTruncated = true
			}
			o := process05ProviderObservation(execution.TaskExecution{RunID: "lane", TaskID: "task", Attempts: 2}, result, nil, "unknown")
			raw, _ := json.Marshal(o)
			var shown map[string]any
			json.Unmarshal(raw, &shown)
			counts, _ := shown["cli_events"].(map[string]any)
			if kind == "complete-error" || kind == "text-only" {
				if counts == nil || counts["basis"] != "native_cli_event" {
					t.Fatal("captured native events not visible")
				}
				expected := float64(0)
				if kind == "complete-error" {
					expected = 2
				}
				if counts["tool_use_events"] != expected {
					t.Fatal("wrong tool emission count")
				}
				if strings.Contains(string(raw), "sensitive-") || strings.Contains(string(raw), "not retained") {
					t.Fatal("raw native data crossed metadata boundary")
				}
				if counts["permission_events"] != nil {
					t.Fatal("unreported permissions claimed known")
				}
			} else if counts != nil {
				t.Fatal("unavailable telemetry asserted zero or forged tool effects")
			}
		})
	}
}
