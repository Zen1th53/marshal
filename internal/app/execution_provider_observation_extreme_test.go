//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"strings"
	"testing"
	"time"
)

func assertNativeProviderObservation(t *testing.T, task execution.TaskExecution, result execution.TaskResult) {
	t.Helper()
	encoded, err := json.Marshal(result.EvidenceList)
	if err != nil {
		t.Fatal(err)
	}
	var retained []map[string]any
	if err := json.Unmarshal(encoded, &retained); err != nil {
		t.Fatal(err)
	}
	if len(retained) != 1 {
		t.Fatalf("native evidence count = %d", len(retained))
	}
	observed, ok := retained[0]["provider_observation"].(map[string]any)
	if !ok {
		t.Fatal("completed native turn lost provider observation")
	}
	if observed["timing_basis"] != "runtime_native_turn_lifecycle" || observed["thread_id"] != result.NativeTurn.ThreadID || observed["turn_id"] != result.NativeTurn.TurnID || observed["run_id"] != task.RunID {
		t.Fatalf("native observation lost identity or mislabeled clocks: %+v", observed)
	}
	start, err := time.Parse(time.RFC3339Nano, fmt.Sprint(observed["started_at"]))
	if err != nil {
		t.Fatal(err)
	}
	end, err := time.Parse(time.RFC3339Nano, fmt.Sprint(observed["ended_at"]))
	if err != nil || end.Before(start) {
		t.Fatalf("native interval = %v to %v: %v", start, end, err)
	}
}

func assertGovernedProviderEvidence(t *testing.T, run *execution.ExecutionRun) {
	t.Helper()
	if len(run.Evidence) != 1 || run.Evidence[0].ProviderObservation == nil {
		t.Fatalf("governed evidence lost durable observation: %+v", run.Evidence)
	}
	o := run.Evidence[0].ProviderObservation
	task := run.Tasks[run.Evidence[0].TaskID]
	if o.RunID != run.RunID || o.TaskID != task.TaskID || o.CanonicalTaskID != task.CanonicalTaskID || o.Attempt != task.Attempts || o.TimingBasis != "adapter_process" || o.StartedAt == nil || o.EndedAt == nil {
		t.Fatalf("governed identity/clocks lost: %+v", o)
	}
}

func TestPrivateProviderObservationPreservesAdapterEvidenceAndUnknowns(t *testing.T) {
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.FixedZone("local", 3600))
	end := start.Add(time.Second)
	tokens := int64(17)
	task := execution.TaskExecution{RunID: "run", TaskID: "task", CanonicalTaskID: "canonical", Attempts: 2}
	for _, name := range []string{"codex", "claude", "opencode"} {
		t.Run(name, func(t *testing.T) {
			o := process05ProviderObservation(task, adapter.Result{Adapter: name, Model: "model", SessionID: "native-session", StartedAt: start, EndedAt: end, Usage: adapter.Usage{Reported: true, PromptTokens: &tokens}}, nil, "adapter_process")
			if o.Provider != name || o.Model != "model" || o.SessionID != "native-session" || o.RunID != task.RunID || o.TaskID != task.TaskID || o.CanonicalTaskID != task.CanonicalTaskID || o.Attempt != 2 || o.StartedAt == nil || !o.StartedAt.Equal(start) || o.StartedAt.Location() != time.UTC || !o.EndedAt.Equal(end) || o.Usage.PromptTokens == nil || *o.Usage.PromptTokens != 17 || !o.Usage.Reported {
				t.Fatalf("adapter evidence lost: %+v", o)
			}
			if o.Usage.PromptTokens == &tokens {
				t.Fatal("retained usage aliases provider output")
			}
		})
	}
	for _, timing := range []adapter.Result{{}, {StartedAt: start}, {EndedAt: end}, {StartedAt: end, EndedAt: start}} {
		o := process05ProviderObservation(task, timing, nil, "adapter_process")
		if o.StartedAt != nil || o.EndedAt != nil || o.TimingBasis != "unknown" || o.Usage.Reported || o.Usage.PromptTokens != nil {
			t.Fatalf("unknown evidence synthesized: %+v", o)
		}
	}
}

func TestPrivateProviderObservationHandInSurvivesRestartAndRefusesForeignEvidence(t *testing.T) {
	start := time.Now().UTC()
	end := start.Add(time.Second)
	task := execution.TaskExecution{RunID: "run", TaskID: "task", CanonicalTaskID: "canonical", Attempts: 2}
	o := process05ProviderObservation(task, adapter.Result{Adapter: "codex", SessionID: "native-session", StartedAt: start, EndedAt: end}, nil, "adapter_process")
	original := execution.ExecutionRun{RunID: "run", Evidence: []execution.EvidenceRef{{EvidenceID: "ev", TaskID: "task", Status: execution.EvidenceValid, ProviderObservation: o}}}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored execution.ExecutionRun
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	records, err := marshalProcess05Observations(&restored, task)
	if err != nil || len(records) != 1 || !strings.Contains(records[0].Output, "native-session") || !strings.Contains(records[0].Output, "paid inference intervals are not recorded") {
		t.Fatalf("hand-in lost durable evidence: %+v %v", records, err)
	}
	for _, change := range []func(*execution.ProviderObservation){func(o *execution.ProviderObservation) { o.RunID = "foreign" }, func(o *execution.ProviderObservation) { o.TaskID = "foreign" }, func(o *execution.ProviderObservation) { o.CanonicalTaskID = "foreign" }, func(o *execution.ProviderObservation) { o.Attempt++ }, func(o *execution.ProviderObservation) { o.SessionID = strings.Repeat("x", 257) }, func(o *execution.ProviderObservation) { o.SessionID = "new\ncommand" }} {
		copied := *o
		change(&copied)
		restored.Evidence[0].ProviderObservation = &copied
		if _, err := marshalProcess05Observations(&restored, task); err == nil {
			t.Fatalf("foreign/malformed observation accepted: %+v", copied)
		}
	}
	restored.Evidence[0].ProviderObservation = o
	restored.Evidence = make([]execution.EvidenceRef, 17)
	for i := range restored.Evidence {
		restored.Evidence[i] = original.Evidence[0]
	}
	if _, err := marshalProcess05Observations(&restored, task); err == nil {
		t.Fatal("unbounded observation count accepted")
	}
	if records, err := marshalProcess05Observations(&execution.ExecutionRun{}, task); err != nil || len(records) != 0 {
		t.Fatalf("legacy missing observations synthesized: %+v %v", records, err)
	}
}

type completingObservedClaudeStream struct{ fakeLiveClaudeStream }

func (*completingObservedClaudeStream) StartSession(_ context.Context, r adapter.Request) (claude.StreamSession, error) {
	return claude.StreamSession{SessionID: "claude-session", Model: r.Model, Worktree: r.Worktree}, nil
}
func (*completingObservedClaudeStream) StartTurn(context.Context, claude.StreamSession, adapter.Request) (claude.StreamTurn, error) {
	return claude.StreamTurn{SessionID: "claude-session", TurnID: "claude-turn"}, nil
}
func (*completingObservedClaudeStream) WaitTurn(context.Context, claude.StreamTurn) (claude.StreamTurnResult, error) {
	return claude.StreamTurnResult{Status: "completed", FinalText: "safe completion", Usage: adapter.Usage{Reported: true, ModelCalls: 1}}, nil
}
func TestPrivateClaudeNativeObservationPreservesTurnIdentity(t *testing.T) {
	repo := runtimeRepo(t)
	server := &completingObservedClaudeStream{}
	runtime := &Runtime{evidenceSanitizer: evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), claudeStreamTurns: make(map[string]*liveClaudeStreamTurn), claudeStreamNew: func(string, string) claudeStreamClient { return server }}
	task := execution.TaskExecution{RunID: "claude-run", RunRevision: 1, TaskID: "claude-task", CanonicalTaskID: "canonical", Attempts: 1, State: execution.TaskRunning, WorktreePath: repo.Path()}
	attachRunningNativeTestRun(t, runtime, task, repo.Path())
	pkg := execution.ConstraintPackage{TaskID: task.TaskID}
	pkg.Digest = execution.ComputeConstraintDigest(pkg)
	result, handled, err := runtime.executeProcess05ClaudeStream(t.Context(), task, pkg, repo.Path(), claude.New("/prepared/claude", nil), adapter.Request{TaskID: "canonical", Worktree: repo.Path(), Model: "claude-model"}, "test")
	if err != nil || !handled || !result.Success {
		t.Fatalf("native turn failed: %+v %v", result, err)
	}
	assertNativeProviderObservation(t, task, result)
	o := result.EvidenceList[0].ProviderObservation
	if o.SessionID != "claude-session" || !o.Usage.Reported || o.Usage.ModelCalls != 1 {
		t.Fatalf("Claude identity/usage lost: %+v", o)
	}
}

func TestPrivateProviderObservationDecoderRefusesAmbiguousOrUnboundedRecords(t *testing.T) {
	raw := `{"provider":"codex","run_id":"run","task_id":"task","attempt":1,"session_id":"session","timing_basis":"unknown","usage":{"reported":false}}`
	if _, _, err := decodeProviderObservationRecord(marshal.CommandRecord{Output: raw + providerObservationRecordSuffix}); err != nil {
		t.Fatal(err)
	}
	for name, output := range map[string]string{
		"unknown-field":    strings.Replace(raw, `"provider":"codex"`, `"provider":"codex","secret":"refuse"`, 1),
		"duplicate":        strings.Replace(raw, `"provider":"codex"`, `"provider":"foreign","provider":"codex"`, 1),
		"duplicate-nested": strings.Replace(raw, `"reported":false`, `"reported":true,"reported":false`, 1),
		"trailing":         raw + `{}`,
		"oversized":        strings.Repeat("x", 4097),
		"reverse-identity": strings.Replace(raw, `"session"`, `"bad\nid"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeProviderObservationRecord(marshal.CommandRecord{Output: output + providerObservationRecordSuffix}); err == nil {
				t.Fatal("malformed runtime observation accepted")
			}
		})
	}
	for _, record := range []marshal.CommandRecord{{ExitCode: 1, Output: raw + providerObservationRecordSuffix}, {Output: raw}} {
		if _, _, err := decodeProviderObservationRecord(record); err == nil {
			t.Fatal("refused/incomplete observation accepted")
		}
	}
}

func process05ObservedTestEvidence(task execution.TaskExecution) []execution.ExecutionEvidence {
	start, end := time.Now().UTC(), time.Now().UTC().Add(time.Millisecond)
	return []execution.ExecutionEvidence{{EvidenceID: "observed-" + task.TaskID, TaskID: task.TaskID, Status: execution.EvidenceValid, ProviderObservation: process05ProviderObservation(task, adapter.Result{Adapter: "test-harness", SessionID: "mock-session", StartedAt: start, EndedAt: end}, nil, "adapter_process")}}
}
func assertProcess05ObservedTestHandIn(t *testing.T, s *MarshalService, run marshal.Run) {
	t.Helper()
	proof, err := s.runtimeProviderObservationProof(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof) != 1 || !strings.Contains(string(proof[0].Observation), "mock-session") {
		t.Fatalf("wired collection discarded observations: %+v", proof)
	}
}

func TestPrivateAGYFiniteOutcomeProjectionKeepsOtherProvidersUnknown(t *testing.T) {
	task := execution.TaskExecution{RunID: "run", TaskID: "task", Attempts: 2}
	result := adapter.Result{Adapter: "agy", Status: adapter.StatusFailure, ExitCode: 7, NativeEnvelope: "success"}
	observed := process05ProviderObservation(task, result, nil, "adapter_process")
	if observed.ProcessOutcome == nil || observed.ProcessOutcome.ExitCode != 7 || observed.Model != "" || execution.ValidateProviderObservation(observed) != nil {
		t.Fatal("actual AGY finite outcome lost/invalid or requested identity fabricated")
	}
	for _, provider := range []string{"codex", "claude", "opencode"} {
		result.Adapter = provider
		if process05ProviderObservation(task, result, nil, "adapter_process").ProcessOutcome != nil {
			t.Fatal("other provider acquired private AGY metadata")
		}
	}
	result.Adapter = "agy"
	result.NativeEnvelope = ""
	if process05ProviderObservation(task, result, nil, "adapter_process").ProcessOutcome != nil {
		t.Fatal("unavailable telemetry became zero/success")
	}
}
