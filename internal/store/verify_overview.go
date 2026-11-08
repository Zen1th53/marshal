package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// Claim statuses the Verify view shows.
const (
	ClaimSupported   = "SUPPORTED"
	ClaimUnsupported = "UNSUPPORTED"
	ClaimUnknown     = "UNKNOWN"
)

// VerifyOverview is one Marshal run's verification state for the TUI
// Verify view: each task's claims with their evidence, and the run's
// independent verifier and architecture review.
type VerifyOverview struct {
	RunID string
	Tasks []VerifyTask
	// Verifier is the newest independent verifier record; nil before one.
	Verifier *VerifyVerifier
	// Session is the run's newest verification session; nil before one.
	Session *VerifySession
	// Architecture is the newest architecture compatibility review bound
	// to this run; nil before one, and always nil in the default edition.
	Architecture *VerifyArchitecture
}

// VerifyTask is one task of the run and the claims of its newest hand-in.
type VerifyTask struct {
	ID, Title string
	State     marshal.TaskState
	// Worker is the task's worker; WorkerProvider is the provider its
	// newest hand-in names, or the worker when it names none.
	Worker, WorkerProvider string
	// Attempt is the newest hand-in attempt; 0 before the first hand-in,
	// and then Claims is empty.
	Attempt int
	// Reviewer is who checked the hand-in: the cross-reviewer when there is
	// one, else the Marshal's reviewer. ReviewerProvider is the
	// cross-reviewer's provider, empty without a cross-review.
	Reviewer, ReviewerProvider string
	// OtherProvider is set when a cross-reviewer from a provider other
	// than the worker's checked the hand-in.
	OtherProvider bool
	// Verdict is the review's verdict; empty before a review.
	Verdict marshal.Verdict
	Claims  []VerifyClaim
}

// Supported counts the task's SUPPORTED claims.
func (t VerifyTask) Supported() int {
	n := 0
	for _, c := range t.Claims {
		if c.Status == ClaimSupported {
			n++
		}
	}
	return n
}

// VerifyClaim is one claim of a hand-in and what supports it.
type VerifyClaim struct {
	// Status is ClaimSupported, ClaimUnsupported or ClaimUnknown.
	Status string
	Text   string
	// Evidence is the evidence reference: a check id, a file or the
	// review's reason.
	Evidence string
	// CheckedBy is who checked the claim: "runtime" for re-run checks, else
	// the reviewer.
	CheckedBy string
	// Criterion marks an approved acceptance criterion, as against a claim
	// the worker reported.
	Criterion bool
}

// VerifyVerifier is the independent verifier's recorded result.
type VerifyVerifier struct {
	Reviewer, Provider, Verdict, Commit string
	Findings                            []string
	// Error is why the record was refused, if it was.
	Error string
	// SameProvider is set when no provider other than a worker's was free.
	SameProvider bool
	At           time.Time
}

// VerifySession is the run's newest verification session.
type VerifySession struct {
	ID    string
	State string
	At    time.Time
}

// VerifyArchitecture is an architecture compatibility review of the run.
type VerifyArchitecture struct {
	ReceiptID, Outcome, ReasonCode, Provider string
	// Pass, Fail and Unknown count the review's obligation assessments.
	Pass, Fail, Unknown int
	At                  time.Time
}

// VerifyOverview reads one run's verification state. Every read goes
// through a key or an index: the run and its tasks by run id, each task's
// newest hand-in and review by (run, task, attempt), the verifier record
// by the (run_id, sequence) event index and the session by the binding
// index. Nothing scans a table with json_extract.
func (s *Store) VerifyOverview(ctx context.Context, runID string) (VerifyOverview, error) {
	out := VerifyOverview{RunID: runID}
	if s == nil || s.db == nil {
		return out, fmt.Errorf("store database is not configured")
	}
	if runID == "" {
		return out, fmt.Errorf("%w: missing run", model.ErrInvalid)
	}
	var projectID, raw string
	err := s.db.QueryRowContext(ctx, `SELECT project_id, data_json FROM marshal_runs WHERE run_id = ?`, runID).Scan(&projectID, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("%w: marshal_runs", model.ErrNotFound)
	}
	if err != nil {
		return out, err
	}
	var run marshal.Run
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		return out, fmt.Errorf("decode marshal run: %w", err)
	}
	for _, t := range run.Tasks {
		vt, err := s.verifyTask(ctx, runID, t)
		if err != nil {
			return out, err
		}
		out.Tasks = append(out.Tasks, vt)
	}
	if out.Verifier, err = s.verifyVerifier(ctx, runID); err != nil {
		return out, err
	}
	if out.Session, err = s.verifySession(ctx, projectID, runID); err != nil {
		return out, err
	}
	if out.Architecture, err = s.verifyArchitecture(ctx, projectID, runID, run); err != nil {
		return out, err
	}
	return out, nil
}

// newestAttempt reads a task's newest attempt from a per-attempt table
// through its (run_id, task_id, attempt) key.
func (s *Store) newestAttempt(ctx context.Context, table, runID, taskID string, into any) (int, error) {
	var attempt int
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT attempt, data_json FROM `+table+` WHERE run_id = ? AND task_id = ? ORDER BY attempt DESC LIMIT 1`, runID, taskID).Scan(&attempt, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal([]byte(raw), into); err != nil {
		return 0, fmt.Errorf("decode %s: %w", table, err)
	}
	return attempt, nil
}

func (s *Store) verifyTask(ctx context.Context, runID string, t marshal.Task) (VerifyTask, error) {
	vt := VerifyTask{ID: t.PlanTaskID, Title: t.Title, State: t.State, Worker: t.Worker, WorkerProvider: t.Worker}
	var h marshal.HandIn
	attempt, err := s.newestAttempt(ctx, "marshal_handins", runID, t.PlanTaskID, &h)
	if err != nil || attempt == 0 {
		return vt, err
	}
	vt.Attempt = attempt
	if h.Provider != "" {
		vt.WorkerProvider = h.Provider
	}
	var review *marshal.Review
	switch got, err := s.GetMarshalReview(ctx, runID, t.PlanTaskID, attempt); {
	case err == nil:
		review = &got.Value
	case !errors.Is(err, model.ErrNotFound):
		return vt, err
	}
	// The approved criteria: supported only by a passing re-run of the
	// check that covers them at the hand-in's result commit.
	for _, c := range t.Criteria {
		claim := VerifyClaim{Text: c, Criterion: true, CheckedBy: "runtime", Status: ClaimUnknown, Evidence: "no approved check"}
		for _, check := range t.Checks {
			if slices.Contains(check.Criteria, c) {
				claim.Evidence = "check:" + check.Command
				break
			}
		}
		switch marshal.CriterionEvidence(t, h, c).Status {
		case "verified":
			claim.Status = ClaimSupported
		case "failed":
			claim.Status, claim.Evidence = ClaimUnsupported, claim.Evidence+" failed"
		case "incomplete":
			claim.Evidence += " · incomplete"
		default:
			if claim.Evidence != "no approved check" {
				claim.Evidence += " · not run"
			}
		}
		vt.Claims = append(vt.Claims, claim)
	}
	// The worker's own claims: supported only by the review that accepted
	// the hand-in, citing the review's evidence.
	verdict, reviewer, refs, reasons := marshal.Verdict(""), "", []string(nil), []string(nil)
	if review != nil {
		verdict, reviewer, refs, reasons = review.Verdict, review.Reviewer, review.EvidenceRefs, review.Reasons
		if ind := review.Independent; ind != nil {
			reviewer, refs = firstNonBlank(ind.Reviewer, ind.Provider), ind.EvidenceRefs
			vt.ReviewerProvider = ind.Provider
			vt.OtherProvider = ind.Provider != "" && !strings.EqualFold(ind.Provider, vt.WorkerProvider)
			if ind.Verdict != marshal.VerdictAccept {
				verdict, reasons = ind.Verdict, ind.Reasons
			}
		}
		vt.Reviewer, vt.Verdict = reviewer, verdict
	}
	for _, c := range h.Claims {
		claim := VerifyClaim{Text: c, Status: ClaimUnknown, Evidence: "not reviewed yet", CheckedBy: firstNonBlank(reviewer, "—")}
		switch {
		case review == nil:
		case verdict == marshal.VerdictAccept:
			claim.Status, claim.Evidence = ClaimSupported, firstNonBlank(strings.Join(refs, " "), "review accepted")
		case verdict != "":
			claim.Status, claim.Evidence = ClaimUnsupported, firstNonBlank(strings.Join(reasons, "; "), "review "+string(verdict))
		}
		vt.Claims = append(vt.Claims, claim)
	}
	return vt, nil
}

func firstNonBlank(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// verifyVerifier reads the run's newest independent verifier record
// through the (run_id, sequence) index.
func (s *Store) verifyVerifier(ctx context.Context, runID string) (*VerifyVerifier, error) {
	var at, raw string
	err := s.db.QueryRowContext(ctx, `SELECT at, data_json FROM structured_events INDEXED BY structured_events_by_run_sequence
		WHERE run_id = ? AND event_type = ? ORDER BY sequence DESC LIMIT 1`, runID, string(events.EventTypeMarshalVerifierRecorded)).Scan(&at, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var data struct {
		Verifier     marshal.VerifierEvidence `json:"verifier"`
		Error        string                   `json:"verification_error"`
		SameProvider bool                     `json:"verifier_same_provider_fallback"`
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("decode verifier record: %w", err)
	}
	v := &VerifyVerifier{Reviewer: data.Verifier.Reviewer, Provider: data.Verifier.Provider, Verdict: data.Verifier.Verdict, Commit: data.Verifier.Commit, Findings: data.Verifier.Findings, Error: data.Error, SameProvider: data.SameProvider}
	v.At, _ = time.Parse(time.RFC3339Nano, at)
	return v, nil
}

// verifySession reads the run's newest verification session through the
// binding index, whose first column is the project.
func (s *Store) verifySession(ctx context.Context, projectID, runID string) (*VerifySession, error) {
	var v VerifySession
	var at string
	err := s.db.QueryRowContext(ctx, `SELECT verification_id, state, updated_at FROM verification_sessions INDEXED BY idx_verification_sessions_binding
		WHERE project_id = ? AND run_id = ? ORDER BY rowid DESC LIMIT 1`, projectID, runID).Scan(&v.ID, &v.State, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.At, _ = time.Parse(timeLayout, at)
	return &v, nil
}
