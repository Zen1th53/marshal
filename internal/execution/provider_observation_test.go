package execution

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestProviderObservationValidationRefusesMalformedEvidence(t *testing.T) {
	start, end := time.Now().UTC(), time.Now().UTC().Add(time.Second)
	zero := time.Time{}
	negative := int64(-1)
	valid := ProviderObservation{Provider: "codex", RunID: "run", TaskID: "task", TimingBasis: "adapter_process", StartedAt: &start, EndedAt: &end}
	for name, change := range map[string]func(*ProviderObservation){
		"oversized":           func(o *ProviderObservation) { o.SessionID = strings.Repeat("x", 257) },
		"control":             func(o *ProviderObservation) { o.SessionID = "bad\nid" },
		"non-ascii":           func(o *ProviderObservation) { o.Model = "model\u0080" },
		"basis":               func(o *ProviderObservation) { o.TimingBasis = "paid-inference" },
		"half-start":          func(o *ProviderObservation) { o.EndedAt = nil },
		"half-end":            func(o *ProviderObservation) { o.StartedAt = nil },
		"zero":                func(o *ProviderObservation) { o.StartedAt = &zero },
		"reverse":             func(o *ProviderObservation) { o.StartedAt = &end; o.EndedAt = &start },
		"unknown-with-clocks": func(o *ProviderObservation) { o.TimingBasis = "unknown" },
		"attempt":             func(o *ProviderObservation) { o.Attempt = -1 },
		"attempt-basis":       func(o *ProviderObservation) { o.AttemptBasis = "provider_calls" },
		"usage":               func(o *ProviderObservation) { o.Usage.ModelCalls = -1 },
		"tokens":              func(o *ProviderObservation) { o.Usage.PromptTokens = &negative },
	} {
		t.Run(name, func(t *testing.T) {
			o := valid
			change(&o)
			if err := ValidateProviderObservation(&o); err == nil {
				t.Fatalf("malformed metadata accepted: %+v", o)
			}
			if _, err := NewEvidenceOracle().RecordEvidence(ExecutionEvidence{EvidenceID: "ev", ProviderObservation: &o}); err == nil {
				t.Fatal("malformed metadata retained by oracle")
			}
		})
	}
	for _, o := range []*ProviderObservation{nil, &valid, {Provider: "codex", TimingBasis: "unknown"}, {Provider: "claude-stream", TimingBasis: "runtime_native_turn_lifecycle", StartedAt: &start, EndedAt: &end}} {
		if err := ValidateProviderObservation(o); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProviderObservationValidationRefusesInvalidCostAndUnreportedResources(t *testing.T) {
	negative, nan, inf := float64(-1), math.NaN(), math.Inf(1)
	token, cost := int64(1), float64(0)
	for name, usage := range map[string]ProviderUsage{
		"negative-cost": {Reported: true, CostUSD: &negative}, "nan-cost": {Reported: true, CostUSD: &nan}, "infinite-cost": {Reported: true, CostUSD: &inf},
		"unreported-tokens": {PromptTokens: &token}, "unreported-cost": {CostUSD: &cost},
	} {
		t.Run(name, func(t *testing.T) {
			o := ProviderObservation{Provider: "codex", RunID: "run", TaskID: "task", TimingBasis: "unknown", Usage: usage}
			if _, err := NewEvidenceOracle().RecordEvidence(ExecutionEvidence{EvidenceID: "ev", ProviderObservation: &o}); err == nil {
				t.Fatalf("invalid/unreported resources retained: %+v", usage)
			}
		})
	}
}

func TestProviderObservationOracleOwnsImmutableSnapshots(t *testing.T) {
	start, end := time.Now().UTC(), time.Now().UTC().Add(time.Second)
	prompt, completion, total, cost := int64(4), int64(5), int64(9), float64(0.1)
	original := ProviderObservation{Provider: "codex", RunID: "run", TaskID: "task", SessionID: "native-original", TimingBasis: "adapter_process", StartedAt: &start, EndedAt: &end, Usage: ProviderUsage{Reported: true, PromptTokens: &prompt, CompletionTokens: &completion, TotalTokens: &total, CostUSD: &cost}}
	oracle := NewEvidenceOracle()
	retained, err := oracle.RecordEvidence(ExecutionEvidence{EvidenceID: "ev", RunID: "run", TaskID: "task", ProviderObservation: &original})
	if err != nil {
		t.Fatal(err)
	}
	wantStart, wantEnd := start, end
	mutate := func(o *ProviderObservation) {
		o.SessionID = "tampered"
		*o.StartedAt = time.Time{}
		*o.EndedAt = time.Time{}
		*o.Usage.PromptTokens = 999
		*o.Usage.CompletionTokens = 999
		*o.Usage.TotalTokens = 999
		*o.Usage.CostUSD = -1
	}
	assert := func() {
		t.Helper()
		got, err := oracle.GetEvidence("ev")
		if err != nil {
			t.Fatal(err)
		}
		o := got.ProviderObservation
		if o.SessionID != "native-original" || !o.StartedAt.Equal(wantStart) || !o.EndedAt.Equal(wantEnd) || *o.Usage.PromptTokens != 4 || *o.Usage.CompletionTokens != 5 || *o.Usage.TotalTokens != 9 || *o.Usage.CostUSD != 0.1 {
			t.Fatalf("caller mutation changed retained validated observation: %+v usage=%+v", o, o.Usage)
		}
	}
	mutate(&original)
	assert()
	mutate(retained.ProviderObservation)
	assert()
	fetched, err := oracle.GetEvidence("ev")
	if err != nil {
		t.Fatal(err)
	}
	mutate(fetched.ProviderObservation)
	assert()
	listed := oracle.ListEvidenceForRun("run")
	if len(listed) != 1 {
		t.Fatal("missing run evidence")
	}
	mutate(listed[0].ProviderObservation)
	assert()
}

func TestLegacyProviderObservationCounterMeaningRemainsUnknown(t *testing.T) {
	raw := `{"provider":"codex","run_id":"run","task_id":"task","attempt":2,"timing_basis":"unknown","usage":{"reported":false}}`
	var o ProviderObservation
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatal(err)
	}
	if o.AttemptBasis != "" {
		t.Fatal("legacy count reinterpreted")
	}
	encoded, _ := json.Marshal(o)
	if string(encoded) != raw {
		t.Fatalf("legacy bytes changed: %s", encoded)
	}
}

func TestProviderCLIEventMetadataIsBoundedAndOwned(t *testing.T) {
	o := ProviderObservation{Provider: "opencode", TimingBasis: "unknown", CLIEvents: &ProviderCLIEvents{Basis: "native_cli_event", CapturedEvents: 2, ToolUseEvents: 1, ToolsCompleted: 1, PermissionEvents: &ProviderCLIPermissionEvents{Asked: 1}}}
	oracle := NewEvidenceOracle()
	if _, err := oracle.RecordEvidence(ExecutionEvidence{EvidenceID: "cli", ProviderObservation: &o}); err != nil {
		t.Fatal(err)
	}
	o.CLIEvents.ToolsCompleted = 999
	o.CLIEvents.PermissionEvents.Asked = 999
	got, err := oracle.GetEvidence("cli")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderObservation.CLIEvents.ToolsCompleted != 1 || got.ProviderObservation.CLIEvents.PermissionEvents.Asked != 1 {
		t.Fatal("CLI metadata aliased caller")
	}
	got.ProviderObservation.CLIEvents.ToolUseEvents = 999
	got.ProviderObservation.CLIEvents.PermissionEvents.Rejected = 999
	again, _ := oracle.GetEvidence("cli")
	if again.ProviderObservation.CLIEvents.ToolUseEvents != 1 || again.ProviderObservation.CLIEvents.PermissionEvents.Rejected != 0 {
		t.Fatal("returned metadata aliases owner")
	}
	for _, counts := range []*ProviderCLIEvents{{Basis: "host_tool_effects", CapturedEvents: 1}, {Basis: "native_cli_event"}, {Basis: "native_cli_event", CapturedEvents: 4097}, {Basis: "native_cli_event", CapturedEvents: 1, ToolUseEvents: 1}, {Basis: "native_cli_event", CapturedEvents: 1, PermissionEvents: &ProviderCLIPermissionEvents{Asked: int(^uint(0) >> 1), Rejected: int(^uint(0) >> 1)}}} {
		o.CLIEvents = counts
		if ValidateProviderObservation(&o) == nil {
			t.Fatal("invalid CLI counts accepted")
		}
	}
}

func TestProviderProcessOutcomeRefusesInventedOrContradictoryFacts(t *testing.T) {
	base := ProviderProcessOutcome{Status: "failure", ExitCode: 0, NativeEnvelope: "invalid_json"}
	for _, tc := range []struct {
		name   string
		change func(*ProviderProcessOutcome)
	}{
		{"status", func(p *ProviderProcessOutcome) { p.Status = "invented" }},
		{"envelope", func(p *ProviderProcessOutcome) { p.NativeEnvelope = "raw PRIVATE_SENTINEL" }},
		{"exit", func(p *ProviderProcessOutcome) { p.ExitCode = 256 }},
		{"success_exit", func(p *ProviderProcessOutcome) { p.Status = "success"; p.NativeEnvelope = "success"; p.ExitCode = 2 }},
		{"success_timeout", func(p *ProviderProcessOutcome) { p.Status = "success"; p.NativeEnvelope = "success"; p.TimedOut = true }},
		{"two_context_states", func(p *ProviderProcessOutcome) { p.TimedOut = true; p.Cancelled = true }},
		{"truncated_status", func(p *ProviderProcessOutcome) {
			p.Status = "success"
			p.NativeEnvelope = "success"
			p.OutputTruncated = true
		}},
		{"denied_success", func(p *ProviderProcessOutcome) { p.Status = "success"; p.NativeEnvelope = "denied_actions" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.change(&p)
			o := &ProviderObservation{Provider: "agy", TimingBasis: "unknown", ProcessOutcome: &p}
			if ValidateProviderObservation(o) == nil {
				t.Fatal("invented/contradictory process facts admitted")
			}
		})
	}
	o := &ProviderObservation{Provider: "codex", TimingBasis: "unknown", ProcessOutcome: &base}
	if ValidateProviderObservation(o) == nil {
		t.Fatal("foreign provider acquired AGY outcome")
	}
	o.ProcessOutcome = nil
	encoded, err := json.Marshal(o)
	if err != nil || strings.Contains(string(encoded), "process_outcome") {
		t.Fatal("legacy/default bytes gained telemetry")
	}
}
