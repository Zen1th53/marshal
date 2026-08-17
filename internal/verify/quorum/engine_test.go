package quorum

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestEngineEvaluateTransitionsToSatisfied(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	attestation := Attestation{
		Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "change-1",
		EvidenceID: "evidence-1", Kind: "security", Result: ResultPass,
		ContentDigest: "sha256:change", CreatedAt: created,
	}
	engine := NewEngine(func() time.Time { return created })
	result, err := engine.Evaluate(context.Background(), []Requirement{{
		Kind: "security", Minimum: 1, Independent: true, AllowedRoles: []string{"reviewer"},
	}}, []Attestation{attestation}, Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.State != StateSatisfied || !result.Satisfied || len(result.Accepted) != 1 {
		t.Fatalf("result = %+v, want satisfied with one accepted attestation", result)
	}
}

func TestEngineRejectsStaleAttestationBeforeSatisfiedState(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	engine := NewEngine(func() time.Time { return created })
	attestation := Attestation{
		Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "foreign-change",
		EvidenceID: "evidence-1", Kind: "security", Result: ResultPass,
		ContentDigest: "sha256:foreign", CreatedAt: created,
	}
	result, err := engine.Evaluate(context.Background(), []Requirement{{Kind: "security", Minimum: 1, Independent: true, AllowedRoles: []string{"reviewer"}}}, []Attestation{attestation}, Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"})
	if !errors.Is(err, ErrStaleAttestation) || result.State != StateInvalidated || result.Satisfied {
		t.Fatalf("result=%+v err=%v, want invalidated stale result", result, err)
	}
}

func TestEngineVetoBlocksQuorum(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	engine := NewEngine(func() time.Time { return created })
	attestation := Attestation{
		Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "change-1",
		EvidenceID: "evidence-1", Kind: "security", Result: ResultVeto,
		ContentDigest: "sha256:change", CreatedAt: created,
	}
	result, err := engine.Evaluate(context.Background(), []Requirement{{Kind: "security", Minimum: 1, Independent: true, AllowedRoles: []string{"reviewer"}}}, []Attestation{attestation}, Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"})
	if !errors.Is(err, ErrVeto) || result.State != StateBlocked || result.Satisfied {
		t.Fatalf("result=%+v err=%v, want blocked veto result", result, err)
	}
}

func TestEngineAuthorizedEvaluationFailsClosedWithoutAuthority(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	engine := NewEngine(func() time.Time { return created })
	_, err := engine.EvaluateAuthorized(context.Background(), nil, nil, nil, Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"})
	if !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("EvaluateAuthorized() error = %v, want ErrAuthorityUnavailable", err)
	}
}

type recordingEventSink struct{ events []Event }

func (s *recordingEventSink) Append(_ context.Context, event Event) error {
	s.events = append(s.events, event)
	return nil
}

func TestEngineEmitsSatisfiedEventWithBoundedReferences(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	sink := &recordingEventSink{}
	engine := NewEngineWithEvents(func() time.Time { return created }, sink)
	attestation := Attestation{
		Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "change-1",
		EvidenceID: "evidence-1", Kind: "security", Result: ResultPass,
		ContentDigest: "sha256:change", CreatedAt: created,
	}
	_, err := engine.Evaluate(context.Background(), []Requirement{{Kind: "security", Minimum: 1, Independent: true, AllowedRoles: []string{"reviewer"}}}, []Attestation{attestation}, Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"})
	if err != nil || len(sink.events) != 1 || sink.events[0].Type != EventQuorumSatisfied || sink.events[0].EvidenceID != "evidence-1" {
		t.Fatalf("err=%v events=%+v", err, sink.events)
	}
}

func TestEngineObservabilityRecordsBoundedQuorumMetric(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	metrics := evidence.NewMetricsRecorder()
	engine := NewEngineWithObservability(func() time.Time { return created }, nil, metrics)
	attestation := Attestation{Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "change-1", EvidenceID: "evidence-1", Kind: "security", Result: ResultPass, ContentDigest: "sha256:change", CreatedAt: created}
	if _, err := engine.Evaluate(context.Background(), []Requirement{{Kind: "security", Minimum: 1, Independent: true, AllowedRoles: []string{"reviewer"}}}, []Attestation{attestation}, Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"}); err != nil {
		t.Fatal(err)
	}
	snapshot := metrics.Snapshot()
	if snapshot.Success[evidence.MetricOperationQuorum] != 1 || snapshot.DurationNanoseconds[evidence.MetricOperationQuorum] == 0 {
		t.Fatalf("metrics=%+v", snapshot)
	}
}

func TestEngineObservabilityClassifiesStaleAttestationAsError(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	metrics := evidence.NewMetricsRecorder()
	engine := NewEngineWithObservability(func() time.Time { return created }, nil, metrics)
	attestation := Attestation{Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "change-1", EvidenceID: "evidence-1", Kind: "security", Result: ResultPass, ContentDigest: "sha256:change", CreatedAt: created.Add(time.Hour)}
	_, err := engine.Evaluate(context.Background(), nil, []Attestation{attestation}, Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"})
	if !errors.Is(err, ErrStaleAttestation) {
		t.Fatalf("error=%v, want ErrStaleAttestation", err)
	}
	snapshot := metrics.Snapshot()
	if snapshot.Success[evidence.MetricOperationQuorum] != 0 || snapshot.Errors["VERIFY_INVALID"] != 1 {
		t.Fatalf("metrics=%+v, want one invalid quorum error and no success", snapshot)
	}
}
