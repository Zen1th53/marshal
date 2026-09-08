package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
)

// The constitution command makes Process 00 inspectable from the CLI.
//
// It is deliberately read-only. Every subcommand reports state the runtime
// already holds; none of them can grant, waive, resolve or alter anything. A
// command that could clear a violation from the command line would be a way
// around the very control the violation represents, so the surface that shows
// governance is kept separate from any surface that could weaken it.
func (c command) constitution(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: missing constitution subcommand (version|invariants|decisions|violations)", model.ErrInvalid)
	}
	runtime, err := app.Open(ctx, c.root)
	if err != nil {
		return err
	}
	defer runtime.Close()

	service := runtime.Constitution()
	if service == nil {
		return fmt.Errorf("%w: constitutional layer is unavailable", model.ErrUnavailable)
	}

	switch args[0] {
	case "version":
		return c.constitutionVersion(service)
	case "invariants":
		return c.constitutionInvariants(service)
	case "decisions":
		return c.constitutionDecisions(ctx, service, args[1:])
	case "violations":
		return c.constitutionViolations(ctx, service, args[1:])
	default:
		return fmt.Errorf("%w: unknown constitution subcommand %s", model.ErrInvalid, args[0])
	}
}

func (c command) constitutionVersion(service *app.ConstitutionService) error {
	version := service.Version()
	// The invariant digest is reported alongside the version so a reader can
	// tell which rules were actually in force, not merely which number was
	// claimed. Two builds naming 1.0.0 with different invariant sets are
	// distinguishable here.
	payload := map[string]any{
		"constitution_version": version.String(),
		"invariant_digest":     service.InvariantDigest(),
		"invariant_count":      service.Registry().Len(),
	}
	return c.print(payload, fmt.Sprintf("constitution=%s invariants=%d digest=%s",
		version, service.Registry().Len(), service.InvariantDigest()))
}

func (c command) constitutionInvariants(service *app.ConstitutionService) error {
	invariants := service.Registry().All()
	payload := make([]map[string]any, 0, len(invariants))
	lines := make([]string, 0, len(invariants))
	for _, invariant := range invariants {
		payload = append(payload, map[string]any{
			"id":          string(invariant.ID),
			"article":     invariant.Article,
			"severity":    string(invariant.Severity),
			"reason":      string(invariant.Reason),
			"explanation": invariant.Explanation,
		})
		lines = append(lines, fmt.Sprintf("%-34s %-8s art.%-5s %s",
			invariant.ID, invariant.Severity, invariant.Article, invariant.Explanation))
	}
	return c.print(payload, strings.Join(lines, "\n"))
}

func (c command) constitutionDecisions(ctx context.Context, service *app.ConstitutionService, args []string) error {
	sessionID, err := requireSessionArg(args, "constitution decisions")
	if err != nil {
		return err
	}
	decisions, err := service.SessionDecisions(ctx, sessionID, 0)
	if err != nil {
		return err
	}
	payload := make([]map[string]any, 0, len(decisions))
	lines := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		payload = append(payload, map[string]any{
			"decision_id":    decision.DecisionID,
			"domain":         decision.Domain,
			"action":         decision.Action,
			"actor":          decision.Actor,
			"surface":        decision.Surface,
			"outcome":        decision.Outcome,
			"reason":         decision.Reason,
			"binding_digest": decision.BindingDigest,
			"evaluated_at":   decision.EvaluatedAt,
		})
		lines = append(lines, fmt.Sprintf("%-20s %-22s %-10s %-8s %s",
			decision.Outcome, decision.Reason, decision.Surface, decision.Domain, decision.Action))
	}
	if len(lines) == 0 {
		lines = append(lines, "no constitutional decisions recorded for this session")
	}
	return c.print(payload, strings.Join(lines, "\n"))
}

func (c command) constitutionViolations(ctx context.Context, service *app.ConstitutionService, args []string) error {
	sessionID, err := requireSessionArg(args, "constitution violations")
	if err != nil {
		return err
	}
	violations, err := service.OpenViolations(ctx, sessionID)
	if err != nil {
		return err
	}
	suspended, err := service.SessionSuspended(ctx, sessionID)
	if err != nil {
		return err
	}

	entries := make([]map[string]any, 0, len(violations))
	lines := make([]string, 0, len(violations)+1)
	for _, violation := range violations {
		entries = append(entries, map[string]any{
			"class":       string(violation.Class),
			"invariant":   string(violation.Invariant),
			"response":    string(violation.Response),
			"actor":       violation.Actor,
			"surface":     string(violation.Surface),
			"detected_at": violation.DetectedAt,
		})
		lines = append(lines, fmt.Sprintf("%-26s %-10s %s", violation.Class, violation.Response, violation.Invariant))
	}
	// The suspension state is reported explicitly rather than left to be
	// inferred from the list, because an empty list and a session that is not
	// suspended are different claims and a reader should not have to guess
	// which one they are being shown.
	payload := map[string]any{
		"session_id": sessionID,
		"suspended":  suspended,
		"violations": entries,
	}
	if suspended {
		lines = append(lines, "session is SUSPENDED and cannot continue until these are resolved")
	} else if len(violations) == 0 {
		lines = append(lines, "no open constitutional violations")
	}
	return c.print(payload, strings.Join(lines, "\n"))
}

func requireSessionArg(args []string, command string) (string, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "", fmt.Errorf("%w: %s requires a session ID", model.ErrInvalid, command)
	}
	return args[0], nil
}

// constitutionVersionString is used by the version command so that a single
// build reports one constitution version everywhere it is asked.
func constitutionVersionString() string { return constitution.Current.String() }
