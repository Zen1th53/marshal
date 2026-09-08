package goalintake_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

var sessionTime = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func testSession() goalintake.Session {
	return goalintake.Session{
		ID:        "SESSION-1",
		ProjectID: projectid.ID(testProject),
		Version:   constitution.Current,
		Mode:      goalintake.ModeStandard,
		Provider:  "codex",
		Goal: model.GoalContract{
			ID:              "GOAL-1",
			SessionID:       "SESSION-1",
			Revision:        1,
			OriginalRequest: "Add caching. Do not modify the database schema.",
			DesiredOutcome:  "API responses are cached",
			Risk:            model.R1,
			AuthoritySource: "operator",
			Confirmation:    model.ConfirmationApproved,
			Constraints: []model.Constraint{
				{ID: "C1", Text: "Do not modify the database schema", Source: "user", IsHard: true},
				{ID: "C2", Text: "prefer table-driven tests", Source: "user", IsHard: false},
			},
			DoNotDo: []string{"do not touch the auth module"},
			UnresolvedDecisions: []model.UnresolvedDecision{
				{ID: "A1", Question: "which cache backend?"},
			},
		},
		CreatedAt: sessionTime,
		UpdatedAt: sessionTime,
	}
}

// A session that could not be continued safely is refused rather than stored.
func TestSessionValidationRefusesWhatCannotBeContinued(t *testing.T) {
	for name, mutate := range map[string]func(*goalintake.Session){
		"no identifier":     func(s *goalintake.Session) { s.ID = "" },
		"no project":        func(s *goalintake.Session) { s.ProjectID = "" },
		"malformed project": func(s *goalintake.Session) { s.ProjectID = "PROJECT-local" },
		"no constitution":   func(s *goalintake.Session) { s.Version = constitution.Version{} },
		"no original request": func(s *goalintake.Session) {
			s.Goal.OriginalRequest = ""
		},
		"unknown mode": func(s *goalintake.Session) { s.Mode = "godmode" },
	} {
		t.Run(name, func(t *testing.T) {
			session := testSession()
			mutate(&session)
			if err := session.Validate(); err == nil {
				t.Fatalf("a session with %s was accepted", name)
			}
			if _, err := goalintake.BuildContinuation(session); err == nil {
				t.Fatalf("a continuation was built for a session with %s", name)
			}
		})
	}
	if err := testSession().Validate(); err != nil {
		t.Fatalf("a valid session was refused: %v", err)
	}
}

// The continuation carries what a provider needs, and nothing a provider said.
func TestContinuationCarriesIntentNotConversation(t *testing.T) {
	continuation, err := goalintake.BuildContinuation(testSession())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if continuation.OriginalRequest != "Add caching. Do not modify the database schema." {
		t.Fatalf("the original request was not carried: %q", continuation.OriginalRequest)
	}
	// Hard constraints and do-not-do entries are both restated.
	joined := strings.Join(continuation.HardConstraints, " | ")
	if !strings.Contains(joined, "Do not modify the database schema") {
		t.Fatalf("a hard constraint was not carried: %s", joined)
	}
	if !strings.Contains(joined, "do not touch the auth module") {
		t.Fatalf("a do-not-do entry was not carried: %s", joined)
	}
	// A soft preference is not presented as a constraint.
	if strings.Contains(joined, "table-driven") {
		t.Fatalf("a soft preference was carried as a hard constraint: %s", joined)
	}
	if len(continuation.OpenQuestions) != 1 {
		t.Fatalf("open questions were %v", continuation.OpenQuestions)
	}
	if continuation.Confirmation != model.ConfirmationApproved {
		t.Fatalf("the confirmation state was not carried: %q", continuation.Confirmation)
	}
	if continuation.ProjectID != projectid.ID(testProject) {
		t.Fatal("the project binding was not carried")
	}
}

// Failover preserves everything about the work and changes only the provider.
func TestFailoverPreservesTheGoalAndConstraints(t *testing.T) {
	original := testSession()
	original.ProviderSessionID = "codex-conversation-abc"

	moved, err := original.Failover("claude", "the provider ran out of capacity", sessionTime)
	if err != nil {
		t.Fatalf("failover: %v", err)
	}

	if moved.Provider != "claude" {
		t.Fatalf("failover selected %q", moved.Provider)
	}
	if moved.Goal.OriginalRequest != original.Goal.OriginalRequest {
		t.Fatal("failover altered the user's original request")
	}
	if len(moved.Goal.Constraints) != len(original.Goal.Constraints) {
		t.Fatal("failover dropped constraints")
	}
	if moved.Goal.Confirmation != original.Goal.Confirmation {
		t.Fatal("failover changed what the user had agreed to")
	}
	if moved.Version.Compare(original.Version) != 0 {
		t.Fatal("failover changed the constitution the session is bound to")
	}

	// The previous provider's handle is discarded: it refers to a
	// conversation the new provider cannot see.
	if moved.ProviderSessionID != "" {
		t.Fatalf("failover carried the previous provider's session handle: %q", moved.ProviderSessionID)
	}

	// The continuation rebuilt after failover is identical in substance.
	before, err := goalintake.BuildContinuation(original)
	if err != nil {
		t.Fatal(err)
	}
	after, err := goalintake.BuildContinuation(moved)
	if err != nil {
		t.Fatal(err)
	}
	if before.OriginalRequest != after.OriginalRequest ||
		len(before.HardConstraints) != len(after.HardConstraints) ||
		before.Confirmation != after.Confirmation {
		t.Fatal("a provider change altered what a provider would be told")
	}
}

// A provider change the user cannot account for is one they will discover
// later and mistrust, so every failover records why.
func TestFailoverMustRecordWhy(t *testing.T) {
	session := testSession()
	if _, err := session.Failover("claude", "  ", sessionTime); err == nil {
		t.Fatal("a failover was accepted with no reason")
	}
	if _, err := session.Failover("", "reason", sessionTime); err == nil {
		t.Fatal("a failover was accepted with no destination")
	}
	if _, err := session.Failover("codex", "reason", sessionTime); err == nil {
		t.Fatal("a failover to the current provider was accepted")
	}
}

// Repeated failover accumulates history rather than losing it, and the
// original request survives every hop.
func TestRepeatedFailoverPreservesIntentAndHistory(t *testing.T) {
	session := testSession()
	original := session.Goal.OriginalRequest

	for _, hop := range []struct{ to, reason string }{
		{"claude", "capacity exhausted"},
		{"opencode", "provider unreachable"},
		{"gemini", "governance could not be confirmed"},
	} {
		var err error
		session, err = session.Failover(hop.to, hop.reason, sessionTime)
		if err != nil {
			t.Fatalf("failover to %s: %v", hop.to, err)
		}
	}

	if session.Goal.OriginalRequest != original {
		t.Fatal("the original request eroded across repeated failover")
	}
	if len(session.Failovers) != 3 {
		t.Fatalf("failover history has %d entries, want 3", len(session.Failovers))
	}
	if session.Provider != "gemini" {
		t.Fatalf("final provider is %q", session.Provider)
	}

	summary := session.FailoverSummary()
	for _, expected := range []string{"claude", "opencode", "gemini", "capacity exhausted"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("the summary omits %q: %s", expected, summary)
		}
	}
}

// A session that never moved says so, rather than implying it might have.
func TestUnmovedSessionReportsContinuity(t *testing.T) {
	summary := testSession().FailoverSummary()
	if !strings.Contains(summary, "one provider throughout") {
		t.Fatalf("an unmoved session did not report continuity: %q", summary)
	}
}

// The continuation is rebuilt rather than stored, so it cannot drift from the
// session it describes.
func TestContinuationTracksTheSession(t *testing.T) {
	session := testSession()
	before, err := goalintake.BuildContinuation(session)
	if err != nil {
		t.Fatal(err)
	}

	// The Goal is revised; the continuation must follow.
	session.Goal.DesiredOutcome = "a different reading"
	session.Goal.UnresolvedDecisions = nil
	after, err := goalintake.BuildContinuation(session)
	if err != nil {
		t.Fatal(err)
	}

	if after.Interpretation == before.Interpretation {
		t.Fatal("the continuation did not follow a revised interpretation")
	}
	if len(after.OpenQuestions) != 0 {
		t.Fatal("the continuation carried a question that had been resolved")
	}
	// The original request is unaffected by revision, as everywhere else.
	if after.OriginalRequest != before.OriginalRequest {
		t.Fatal("revising the interpretation changed the original request")
	}
}
