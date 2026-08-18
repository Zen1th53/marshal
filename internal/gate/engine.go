package gate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/policy"
)

type CheckID string

type CheckRequest struct {
	Point        GatePoint
	Subject      string
	Resource     string
	PolicyDigest policy.PolicyDigest
}

type CheckFunc func(context.Context, CheckRequest) (CheckResult, error)

type EngineConfig struct {
	PolicyDigest            policy.PolicyDigest
	RequiredChecks          map[GatePoint][]CheckID
	Checks                  map[CheckID]CheckFunc
	Clock                   func() time.Time
	RequireIndependentCheck bool
}

type Engine struct {
	policyDigest            policy.PolicyDigest
	requiredChecks          map[GatePoint][]CheckID
	checks                  map[CheckID]CheckFunc
	clock                   func() time.Time
	requireIndependentCheck bool
}

func NewEngine(config EngineConfig) (*Engine, error) {
	if err := config.PolicyDigest.Validate(); err != nil {
		return nil, ErrInvalidDecision
	}
	if config.Clock == nil {
		config.Clock = func() time.Time { return time.Now().UTC() }
	}
	engine := &Engine{policyDigest: config.PolicyDigest, checks: make(map[CheckID]CheckFunc), requiredChecks: make(map[GatePoint][]CheckID), clock: config.Clock, requireIndependentCheck: config.RequireIndependentCheck}
	for id, check := range config.Checks {
		if strings.TrimSpace(string(id)) == "" || check == nil {
			return nil, ErrUnknownCheck
		}
		engine.checks[id] = check
	}
	for point, required := range config.RequiredChecks {
		if !validGatePoint(point) || len(required) == 0 {
			return nil, ErrInvalidDecision
		}
		for _, id := range required {
			if strings.TrimSpace(string(id)) == "" {
				return nil, ErrUnknownCheck
			}
			engine.requiredChecks[point] = append(engine.requiredChecks[point], id)
		}
	}
	return engine, nil
}

func (e *Engine) Evaluate(ctx context.Context, point GatePoint, subject, resource string) (Decision, error) {
	decision := Decision{DecisionID: decisionID(point, subject, resource, e.policyDigest), Point: point, Subject: subject, Resource: resource, PolicyDigest: e.policyDigest, CreatedAt: e.clock(), State: DecisionStateEvaluating}
	if err := ctx.Err(); err != nil {
		decision.State = DecisionStateBlocked
		return decision, err
	}
	if !validGatePoint(point) {
		decision.State = DecisionStateBlocked
		return decision, ErrUnknownGatePoint
	}
	if strings.TrimSpace(subject) == "" || strings.TrimSpace(resource) == "" {
		decision.State = DecisionStateBlocked
		return decision, ErrInvalidDecision
	}
	required := e.requiredChecks[point]
	if len(required) == 0 {
		decision.State = DecisionStateDenied
		return decision, ErrRequiredCheckMissing
	}
	for _, id := range required {
		check, ok := e.checks[id]
		if !ok {
			decision.State = DecisionStateBlocked
			return decision, ErrUnknownCheck
		}
		result, err := check(ctx, CheckRequest{Point: point, Subject: subject, Resource: resource, PolicyDigest: e.policyDigest})
		result.CheckID = string(id)
		decision.Checks = append(decision.Checks, result)
		if err != nil {
			decision.State = DecisionStateBlocked
			return decision, ErrPolicyDeny
		}
		if result.EvidenceExpiresAt != nil && !e.clock().Before(*result.EvidenceExpiresAt) {
			decision.State = DecisionStateDenied
			return decision, ErrStaleEvidence
		}
		if e.requireIndependentCheck && result.VerifierID == subject {
			decision.State = DecisionStateDenied
			return decision, ErrPolicyDeny
		}
		if result.Status != CheckStatusPass {
			decision.State = DecisionStateDenied
			return decision, ErrPolicyDeny
		}
	}
	decision.Allowed = true
	decision.State = DecisionStateAllowed
	return decision, nil
}

func decisionID(point GatePoint, subject, resource string, digest policy.PolicyDigest) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s", point, subject, resource, digest)))
	return "gate-decision-" + hex.EncodeToString(sum[:])
}
