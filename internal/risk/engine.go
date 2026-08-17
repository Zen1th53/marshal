package risk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

type AssessmentRepository interface {
	PutRiskAssessment(context.Context, Assessment) error
	GetRiskAssessment(context.Context, AssessmentID) (Assessment, error)
	TransitionRiskAssessmentState(context.Context, AssessmentID, AssessmentState, AssessmentState) error
}

type Authority interface {
	AuthorizeRisk(context.Context, Assessment) error
}

type AssessmentRequest struct {
	ID           AssessmentID
	Descriptor   ToolDescriptor
	PolicyDigest PolicyDigest
}

func (r AssessmentRequest) Validate() error {
	if !safeText(string(r.ID)) {
		return ErrDescriptorInvalid
	}
	if err := r.Descriptor.Validate(); err != nil {
		return err
	}
	if r.PolicyDigest != "" && !safeText(string(r.PolicyDigest)) {
		return ErrDescriptorInvalid
	}
	return nil
}

type Engine struct {
	repository AssessmentRepository
	authority  Authority
	eventStore events.Store
	metrics    *evidence.MetricsRecorder
}

func NewEngine(repository AssessmentRepository) *Engine {
	return NewAuthorizedEngine(repository, nil)
}

func NewAuthorizedEngine(repository AssessmentRepository, authority Authority) *Engine {
	return &Engine{repository: repository, authority: authority}
}

func NewAuditedEngine(repository AssessmentRepository, authority Authority, eventStore events.Store) *Engine {
	engine := NewAuthorizedEngine(repository, authority)
	engine.eventStore = eventStore
	return engine
}

func NewObservedEngine(repository AssessmentRepository, authority Authority, metrics *evidence.MetricsRecorder) *Engine {
	engine := NewAuthorizedEngine(repository, authority)
	engine.metrics = metrics
	return engine
}

// Require delegates authorization to the existing authority boundary. An
// assessment's level and score only produce requirements; they never grant
// permission on their own.
func (e *Engine) Require(ctx context.Context, assessment Assessment) error {
	if e == nil || e.authority == nil {
		return ErrAuthorizationUnavailable
	}
	if assessment.State != StateRequirementsEmitted {
		return ErrDescriptorInvalid
	}
	if err := assessment.Validate(); err != nil {
		return err
	}
	if err := e.authority.AuthorizeRisk(ctx, assessment); err != nil {
		_ = e.emitEvent(ctx, assessment, events.Type("risk.override.denied"), "denied")
		if errors.Is(err, ErrAuthorizationDenied) {
			return ErrAuthorizationDenied
		}
		return ErrAuthorizationDenied
	}
	return nil
}

func (e *Engine) Assess(ctx context.Context, request AssessmentRequest) (Assessment, error) {
	started := time.Now()
	var resultErr error
	defer func() { e.observe(resultErr, time.Since(started)) }()
	if e == nil || e.repository == nil {
		resultErr = fmt.Errorf("%w: risk assessment repository is unavailable", ErrDescriptorInvalid)
		return Assessment{}, resultErr
	}
	if err := request.Validate(); err != nil {
		resultErr = err
		return Assessment{}, err
	}
	if existing, err := e.repository.GetRiskAssessment(ctx, request.ID); err == nil {
		if existing.ActionDigest != actionDigest(request.Descriptor) || existing.PolicyDigest != request.PolicyDigest {
			resultErr = fmt.Errorf("%w: risk assessment identity is immutable", ErrDescriptorInvalid)
			return Assessment{}, resultErr
		}
		if err := e.emitAssessmentEvents(ctx, existing, request.Descriptor.Resource); err != nil {
			resultErr = err
			return Assessment{}, err
		}
		return existing, nil
	} else if !errors.Is(err, model.ErrNotFound) {
		resultErr = fmt.Errorf("%w: risk assessment lookup unavailable", model.ErrUnavailable)
		return Assessment{}, resultErr
	}

	assessment := classify(request)
	if request.Descriptor.ClaimedLevel != "" && request.Descriptor.ClaimedLevel.Rank() < assessment.Level.Rank() {
		resultErr = ErrDowngradeForbidden
		return Assessment{}, resultErr
	}
	if err := e.repository.PutRiskAssessment(ctx, assessment); err != nil {
		resultErr = err
		return Assessment{}, err
	}
	advanced, err := e.advanceToRequirements(ctx, assessment.ID)
	if err != nil {
		resultErr = err
		return Assessment{}, err
	}
	assessment = advanced
	if err := e.emitAssessmentEvents(ctx, assessment, request.Descriptor.Resource); err != nil {
		resultErr = err
		return Assessment{}, err
	}
	return assessment, nil
}

func (e *Engine) observe(err error, duration time.Duration) {
	if e == nil || e.metrics == nil {
		return
	}
	result := evidence.MetricResultSuccess
	reason := ""
	if err != nil {
		result = evidence.MetricResultError
		if errors.Is(err, ErrDescriptorInvalid) {
			result = evidence.MetricResultInvalid
			reason = string(CodeDescriptorInvalid)
		} else if errors.Is(err, ErrDowngradeForbidden) {
			result = evidence.MetricResultDenied
			reason = string(CodeDowngradeForbidden)
		} else if errors.Is(err, model.ErrConflict) {
			result = evidence.MetricResultConflict
			reason = "SQLITE_BUSY"
		} else if errors.Is(err, context.Canceled) {
			result = evidence.MetricResultCancelled
		} else {
			reason = "RISK_ERROR"
		}
	}
	e.metrics.Observe(evidence.MetricOperationRisk, result, reason, duration)
}

func (e *Engine) advanceToRequirements(ctx context.Context, id AssessmentID) (Assessment, error) {
	for attempt := 0; attempt < 32; attempt++ {
		assessment, err := e.repository.GetRiskAssessment(ctx, id)
		if err != nil {
			return Assessment{}, err
		}
		switch assessment.State {
		case StateRequirementsEmitted:
			return assessment, nil
		case StateRequested:
			err = e.repository.TransitionRiskAssessmentState(ctx, id, StateRequested, StateClassified)
		case StateClassified:
			err = e.repository.TransitionRiskAssessmentState(ctx, id, StateClassified, StateRequirementsEmitted)
		default:
			return Assessment{}, ErrDescriptorInvalid
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, model.ErrConflict) {
			return Assessment{}, err
		}
	}
	return Assessment{}, fmt.Errorf("%w: risk assessment state reconciliation exceeded retry bound", model.ErrConflict)
}

func (e *Engine) emitAssessmentEvents(ctx context.Context, assessment Assessment, resource string) error {
	if e.eventStore == nil {
		return nil
	}
	if err := e.emitEventWithResource(ctx, assessment, resource, events.Type("risk.assessment.created"), "classified"); err != nil {
		return err
	}
	levelEvent := events.Type("")
	switch assessment.Level {
	case LevelHigh:
		levelEvent = events.Type("risk.level.high")
	case LevelCritical:
		levelEvent = events.Type("risk.level.critical")
	}
	if levelEvent != "" {
		return e.emitEventWithResource(ctx, assessment, resource, levelEvent, string(assessment.Level))
	}
	return nil
}

func (e *Engine) emitEvent(ctx context.Context, assessment Assessment, eventType events.Type, result string) error {
	return e.emitEventWithResource(ctx, assessment, "", eventType, result)
}

func (e *Engine) emitEventWithResource(ctx context.Context, assessment Assessment, resource string, eventType events.Type, result string) error {
	if e.eventStore == nil {
		return nil
	}
	key := string(eventType) + "/" + string(assessment.ID)
	_, err := e.eventStore.Append(ctx, events.Event{
		ID:         events.EventID("RISK-" + hex.EncodeToString(sha256Bytes([]byte(key)))),
		Type:       eventType,
		Subject:    events.SubjectID("risk-engine"),
		ResourceID: events.ResourceID(resource),
		Data: map[string]string{
			"assessment_id": string(assessment.ID),
			"level":         string(assessment.Level),
			"result":        result,
			"policy_digest": string(assessment.PolicyDigest),
			"resource":      resource,
		},
		IdempotencyKey: events.IdempotencyKey(key),
	})
	return err
}

func sha256Bytes(value []byte) []byte {
	sum := sha256.Sum256(value)
	return sum[:]
}

func classify(request AssessmentRequest) Assessment {
	factors := request.Descriptor.Factors
	level := LevelLow
	score := 0
	if factors.ScopeBreadth > 0 {
		score += factors.ScopeBreadth
		level = LevelMedium
	}
	for _, factor := range []struct {
		active bool
		weight int
	}{
		{factors.ExternalWrite, 3},
		{factors.Destructive, 4},
		{factors.SecretUse, 3},
		{factors.Network, 2},
		{factors.PrivilegeEscalation, 5},
		{factors.Deploy, 5},
	} {
		if factor.active {
			score += factor.weight
			if factor.weight >= 5 {
				level = LevelCritical
			} else if level.Rank() < LevelHigh.Rank() {
				level = LevelHigh
			}
		}
	}
	if !knownAction(request.Descriptor.Action) && factors.ExternalWrite {
		level = LevelHigh
	}
	requirements := requirementsFor(level, factors)
	return Assessment{
		ID:                   request.ID,
		ActionDigest:         actionDigest(request.Descriptor),
		Level:                level,
		Score:                score,
		Factors:              factors,
		RequiredAuthorities:  requirements.authorities,
		RequiredCapabilities: requirements.capabilities,
		PolicyDigest:         request.PolicyDigest,
		State:                StateRequested,
	}
}

type requirements struct {
	authorities  []string
	capabilities []string
}

func requirementsFor(level Level, factors Factors) requirements {
	result := requirements{}
	if level.Rank() >= LevelHigh.Rank() {
		result.capabilities = append(result.capabilities, "tool.risk.high")
	}
	if level == LevelCritical || factors.PrivilegeEscalation || factors.Deploy {
		result.authorities = append(result.authorities, "owner.approval")
	}
	return result
}

func knownAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "read", "list", "status", "test", "go.test", "git.push", "git.commit", "shell.execute", "deploy", "mcp.call":
		return true
	default:
		return false
	}
}

func actionDigest(descriptor ToolDescriptor) ActionDigest {
	canonical, _ := json.Marshal(struct {
		Tool     string  `json:"tool"`
		Action   string  `json:"action"`
		Resource string  `json:"resource"`
		Factors  Factors `json:"factors"`
	}{descriptor.Tool, strings.ToLower(strings.TrimSpace(descriptor.Action)), descriptor.Resource, descriptor.Factors})
	sum := sha256.Sum256(canonical)
	return ActionDigest("sha256:" + hex.EncodeToString(sum[:]))
}
