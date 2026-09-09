package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// ConstitutionService is the runtime entry point to Process 00.
//
// It is the only place in MARSHAL that turns a requested action into a
// constitutional verdict, and every surface reaches it through the same
// method. That single funnel is what makes "the same material action has the
// same authority on every surface" structural rather than a property each
// surface has to remember to preserve.
type ConstitutionService struct {
	runtime  *Runtime
	registry *constitution.Registry
	now      func() time.Time
}

// Constitution returns the runtime's constitutional service.
func (r *Runtime) Constitution() *ConstitutionService {
	if r == nil {
		return nil
	}
	return &ConstitutionService{
		runtime:  r,
		registry: constitution.Default(),
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// Version reports the constitution version this runtime implements.
func (s *ConstitutionService) Version() constitution.Version {
	if s == nil || s.registry == nil {
		return constitution.Version{}
	}
	return s.registry.Version()
}

// Registry exposes the invariant registry for reporting surfaces.
func (s *ConstitutionService) Registry() *constitution.Registry {
	if s == nil {
		return nil
	}
	return s.registry
}

// InvariantDigest fingerprints the active invariant set, so a report can prove
// which rules were in force rather than merely naming a version number.
func (s *ConstitutionService) InvariantDigest() string {
	if s == nil || s.registry == nil {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(s.registry.Version().String()))
	for _, inv := range s.registry.All() {
		h.Write([]byte(inv.ID))
		h.Write([]byte{0})
		h.Write([]byte(inv.Severity))
		h.Write([]byte{0})
		h.Write([]byte(inv.Reason))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// BindSession records the constitution version a session operates under.
//
// The binding is written once and never updated. A session that outlives a
// runtime upgrade continues to be evaluated against the version it started
// under, which is what stops an upgrade from silently reinterpreting decisions
// already made (Article XXII).
func (s *ConstitutionService) BindSession(ctx context.Context, sessionID, projectID string, mode constitution.Mode) (constitution.Version, error) {
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return constitution.Version{}, fmt.Errorf("%w: constitution service is unavailable", model.ErrUnavailable)
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(projectID) == "" {
		return constitution.Version{}, fmt.Errorf("%w: session and project are required to bind a constitution", model.ErrInvalid)
	}
	if mode != constitution.ModeStandard && mode != constitution.ModeUltra {
		return constitution.Version{}, fmt.Errorf("%w: unknown session mode %q", model.ErrInvalid, mode)
	}

	version := s.registry.Version()
	err := s.runtime.store.BindSessionConstitution(ctx, store.SessionConstitution{
		SessionID:       sessionID,
		ProjectID:       projectID,
		Version:         version.String(),
		InvariantDigest: s.InvariantDigest(),
		Mode:            string(mode),
		BoundAt:         s.now(),
	})
	if err != nil {
		return constitution.Version{}, err
	}
	return version, nil
}

// SessionVersion returns the constitution version a session is bound to.
func (s *ConstitutionService) SessionVersion(ctx context.Context, sessionID string) (constitution.Version, bool, error) {
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return constitution.Version{}, false, fmt.Errorf("%w: constitution service is unavailable", model.ErrUnavailable)
	}
	binding, found, err := s.runtime.store.GetSessionConstitution(ctx, sessionID)
	if err != nil || !found {
		return constitution.Version{}, found, err
	}
	version, parseErr := constitution.ParseVersion(binding.Version)
	if parseErr != nil {
		return constitution.Version{}, false, fmt.Errorf(
			"stored constitution version for session %s is unreadable: %w", sessionID, parseErr)
	}
	return version, true, nil
}

// DecideRequest asks for a constitutional verdict on a material action.
type DecideRequest struct {
	Envelope constitution.Envelope
	State    constitution.RuntimeState
	// Advisory is optional model input. It is advisory in the strict sense:
	// the verdict is reached without it, and it can only make the outcome more
	// restrictive.
	Advisory *constitution.Advisory
}

// DecideResult carries the verdict together with any violations it evidenced.
type DecideResult struct {
	Verdict    constitution.Verdict     `json:"verdict"`
	Violations []constitution.Violation `json:"violations,omitempty"`
	// Response is the strongest response the violations require, if any.
	Response constitution.Response `json:"response,omitempty"`
}

// Permitted reports whether the action may proceed.
func (r DecideResult) Permitted() bool { return r.Verdict.Outcome.Permits() }

// Decide evaluates one material action and records the outcome.
//
// Persistence is part of the decision rather than a side effect of it: a
// verdict that was not recorded cannot be audited, and an approval binding
// that was not stored cannot be checked for replay later. A storage failure
// therefore fails the decision, so MARSHAL never proceeds on authority it
// cannot afterwards account for.
func (s *ConstitutionService) Decide(ctx context.Context, request DecideRequest) (DecideResult, error) {
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return DecideResult{}, fmt.Errorf("%w: constitution service is unavailable", model.ErrUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return DecideResult{}, err
	}

	envelope := request.Envelope
	state := request.State

	// The runtime always supplies its own constitution version and clock. A
	// caller cannot claim to run under different rules than this build
	// implements.
	state.RuntimeConstitution = s.registry.Version()
	if state.Now.IsZero() {
		state.Now = s.now()
	}
	// Entitlement is read from the runtime's ULTRA gate, never from the
	// request. A caller that could assert its own entitlement could grant
	// itself ULTRA by filling in a struct field, which would make the
	// cryptographic lease decorative. With no gate attached this is false,
	// so an unconfigured runtime evaluates ULTRA envelopes as unentitled.
	state.EntitlementValid = s.runtime.ULTRAEntitled()
	// A session's recorded binding outranks whatever version the caller put in
	// the envelope, so a surface cannot shop for laxer semantics by asserting
	// an older version.
	if bound, found, err := s.SessionVersion(ctx, envelope.SessionID); err == nil && found {
		envelope.ConstitutionVersion = bound
	}

	verdict := constitution.Evaluate(s.registry, envelope, state, request.Advisory)
	violations := constitution.ViolationsFrom(verdict, envelope, state.Now)

	result := DecideResult{Verdict: verdict, Violations: violations}
	if response, ok := constitution.MostSevereResponse(violations); ok {
		result.Response = response
	}

	if err := s.record(ctx, envelope, result); err != nil {
		return DecideResult{}, err
	}
	return result, nil
}

func (s *ConstitutionService) record(ctx context.Context, envelope constitution.Envelope, result DecideResult) error {
	findings, err := json.Marshal(result.Verdict.Findings)
	if err != nil {
		return fmt.Errorf("encode constitutional findings: %w", err)
	}

	decision := store.ConstitutionalDecisionRow{
		DecisionID:     envelope.DecisionID,
		ProjectID:      envelope.ProjectID,
		SessionID:      envelope.SessionID,
		Version:        result.Verdict.ConstitutionVersion.String(),
		Process:        envelope.Process,
		Domain:         string(envelope.Domain),
		Action:         envelope.Action,
		Actor:          envelope.Actor,
		Surface:        string(envelope.Surface),
		Mode:           string(envelope.Mode),
		Outcome:        string(result.Verdict.Outcome),
		Reason:         string(result.Verdict.Reason),
		BindingDigest:  result.Verdict.BindingDigest,
		StateDigest:    envelope.StateDigest,
		AdvisoryStatus: string(result.Verdict.AdvisoryStatus),
		FindingsJSON:   string(findings),
		EvaluatedAt:    result.Verdict.EvaluatedAt,
	}

	violations := make([]store.ConstitutionalViolationRow, 0, len(result.Violations))
	for i, violation := range result.Violations {
		violations = append(violations, store.ConstitutionalViolationRow{
			ViolationID: fmt.Sprintf("%s:%d:%s", envelope.DecisionID, i, violation.Class),
			ProjectID:   violation.ProjectID,
			SessionID:   violation.SessionID,
			DecisionID:  violation.DecisionID,
			Class:       string(violation.Class),
			InvariantID: string(violation.Invariant),
			Response:    string(violation.Response),
			Actor:       violation.Actor,
			Surface:     string(violation.Surface),
			Version:     violation.Version.String(),
			Detail:      violation.Detail,
			DetectedAt:  violation.DetectedAt,
		})
	}
	return s.runtime.store.RecordConstitutionalDecision(ctx, decision, violations)
}

// OpenViolations returns a session's unresolved violations.
func (s *ConstitutionService) OpenViolations(ctx context.Context, sessionID string) ([]constitution.Violation, error) {
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return nil, fmt.Errorf("%w: constitution service is unavailable", model.ErrUnavailable)
	}
	rows, err := s.runtime.store.ListOpenConstitutionalViolations(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	violations := make([]constitution.Violation, 0, len(rows))
	for _, row := range rows {
		violation := constitution.Violation{
			Class:      constitution.ViolationClass(row.Class),
			Invariant:  constitution.InvariantID(row.InvariantID),
			Response:   constitution.Response(row.Response),
			ProjectID:  row.ProjectID,
			SessionID:  row.SessionID,
			DecisionID: row.DecisionID,
			Actor:      row.Actor,
			Surface:    constitution.Surface(row.Surface),
			Detail:     row.Detail,
			DetectedAt: row.DetectedAt,
		}
		if parsed, parseErr := constitution.ParseVersion(row.Version); parseErr == nil {
			violation.Version = parsed
		}
		violations = append(violations, violation)
	}
	return violations, nil
}

// SessionSuspended reports whether a session has an unresolved violation that
// requires it to stop. Callers must consult this before resuming work, so a
// suspended session cannot be restarted into by simply asking again.
func (s *ConstitutionService) SessionSuspended(ctx context.Context, sessionID string) (bool, error) {
	violations, err := s.OpenViolations(ctx, sessionID)
	if err != nil {
		return false, err
	}
	for _, violation := range violations {
		if violation.Response.Halting() {
			return true, nil
		}
	}
	return false, nil
}

// SessionDecisions returns a session's recorded constitutional decisions.
func (s *ConstitutionService) SessionDecisions(ctx context.Context, sessionID string, limit int) ([]store.ConstitutionalDecisionRow, error) {
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return nil, fmt.Errorf("%w: constitution service is unavailable", model.ErrUnavailable)
	}
	return s.runtime.store.ListConstitutionalDecisions(ctx, sessionID, limit)
}
