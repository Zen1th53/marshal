package quorum

import (
	"context"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

type Engine struct {
	now       func() time.Time
	eventSink EventSink
	metrics   *evidence.MetricsRecorder
}

func NewEngineWithObservability(now func() time.Time, sink EventSink, metrics *evidence.MetricsRecorder) *Engine {
	engine := NewEngineWithEvents(now, sink)
	engine.metrics = metrics
	return engine
}

func NewEngineWithEvents(now func() time.Time, sink EventSink) *Engine {
	engine := NewEngine(now)
	engine.eventSink = sink
	return engine
}

type Authority interface {
	Authorize(context.Context, Provenance) error
}

func NewEngine(now func() time.Time) *Engine {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Engine{now: now}
}

func (e *Engine) Evaluate(ctx context.Context, requirements []Requirement, attestations []Attestation, provenance Provenance) (Evaluation, error) {
	started := time.Now()
	var result Evaluation
	var resultErr error
	defer func() {
		if e.metrics == nil {
			return
		}
		metricResult := evidence.MetricResultSuccess
		reason := "VERIFY_QUORUM_SATISFIED"
		if resultErr != nil {
			metricResult = evidence.MetricResultError
			reason = "VERIFY_INVALID"
		} else if !result.Satisfied {
			metricResult = evidence.MetricResultDenied
			reason = "VERIFY_QUORUM_UNMET"
		}
		e.metrics.Observe(evidence.MetricOperationQuorum, metricResult, reason, time.Since(started))
	}()
	if err := ctx.Err(); err != nil {
		result = Evaluation{State: StateInvalidated}
		resultErr = err
		return result, err
	}
	if err := provenance.Validate(); err != nil {
		result = Evaluation{State: StateInvalidated}
		resultErr = err
		return result, err
	}
	for _, requirement := range requirements {
		if err := requirement.Validate(); err != nil {
			result = Evaluation{State: StateInvalidated}
			resultErr = err
			return result, err
		}
	}
	result = Evaluation{State: StatePending}
	seen := make(map[string]struct{}, len(attestations))
	for _, attestation := range attestations {
		if err := attestation.Validate(); err != nil {
			result = Evaluation{State: StateInvalidated}
			resultErr = err
			return result, err
		}
		if attestation.ChangeID != provenance.ChangeID || attestation.ContentDigest != provenance.ContentDigest {
			result = Evaluation{State: StateInvalidated}
			resultErr = ErrStaleAttestation
			return result, resultErr
		}
		if attestation.InvalidatedAt != nil || !attestation.CreatedAt.Before(e.now().Add(time.Nanosecond)) {
			result = Evaluation{State: StateInvalidated}
			resultErr = ErrStaleAttestation
			return result, resultErr
		}
		if attestation.Result == ResultVeto {
			result.Rejected = append(result.Rejected, attestation)
			result.State = StateBlocked
			if err := e.emit(ctx, Event{Type: EventQuorumBlocked, ChangeID: provenance.ChangeID, Principal: attestation.Subject, EvidenceID: attestation.EvidenceID, ContentDigest: provenance.ContentDigest, State: result.State, Reason: string(ErrVeto.Error())}); err != nil {
				resultErr = err
				return result, err
			}
			resultErr = ErrVeto
			return result, resultErr
		}
		key := strings.TrimSpace(attestation.Subject) + "\x00" + string(attestation.Kind)
		if _, exists := seen[key]; exists {
			result = Evaluation{State: StateInvalidated}
			resultErr = ErrDuplicatePrincipal
			return result, resultErr
		}
		seen[key] = struct{}{}
		result.Accepted = append(result.Accepted, attestation)
	}
	for _, requirement := range requirements {
		count := 0
		for _, attestation := range result.Accepted {
			if attestation.Kind != requirement.Kind || attestation.Result != ResultPass || !matches(requirement, attestation) {
				continue
			}
			count++
		}
		if count < requirement.Minimum {
			result.Missing = append(result.Missing, requirement)
		}
	}
	if len(result.Missing) == 0 {
		result.State = StateSatisfied
		result.Satisfied = true
	} else if len(result.Accepted) > 0 {
		result.State = StatePartiallySatisfied
	}
	event := Event{Type: eventTypeForState(result.State), ChangeID: provenance.ChangeID, ContentDigest: provenance.ContentDigest, State: result.State}
	if len(result.Accepted) > 0 {
		event.Principal = result.Accepted[0].Subject
		event.EvidenceID = result.Accepted[0].EvidenceID
	}
	if err := e.emit(ctx, event); err != nil {
		resultErr = err
		return result, err
	}
	return result, nil
}

func (e *Engine) emit(ctx context.Context, event Event) error {
	if e.eventSink == nil {
		return nil
	}
	return e.eventSink.Append(ctx, event)
}

func eventTypeForState(state QuorumState) EventType {
	switch state {
	case StateSatisfied:
		return EventQuorumSatisfied
	case StatePartiallySatisfied:
		return EventQuorumPartial
	case StateBlocked:
		return EventQuorumBlocked
	default:
		return EventQuorumInvalidated
	}
}

func (e *Engine) EvaluateAuthorized(ctx context.Context, authority Authority, requirements []Requirement, attestations []Attestation, provenance Provenance) (Evaluation, error) {
	if err := ctx.Err(); err != nil {
		return Evaluation{State: StateInvalidated}, err
	}
	if authority == nil {
		return Evaluation{State: StateInvalidated}, ErrAuthorityUnavailable
	}
	if err := authority.Authorize(ctx, provenance); err != nil {
		return Evaluation{State: StateInvalidated}, err
	}
	return e.Evaluate(ctx, requirements, attestations, provenance)
}

func matches(requirement Requirement, attestation Attestation) bool {
	roleOK := len(requirement.AllowedRoles) == 0 || contains(requirement.AllowedRoles, attestation.Role)
	providerOK := len(requirement.AllowedProviders) == 0 || contains(requirement.AllowedProviders, attestation.Provider)
	return roleOK && providerOK
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
