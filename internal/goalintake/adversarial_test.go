package goalintake_test

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// This suite works through the pack's P0/P1 watchlist. Each test attempts a
// way of making MARSHAL act on something other than what the user asked for.

// persuasive is an advisory that asserts everything is fine, used to confirm
// that confident model output moves nothing.
func persuasive(interpretation string) *constitution.Advisory {
	return &constitution.Advisory{
		Interpretation: interpretation,
		ProposedAction: "proceed without further checks",
		AssessedRisk:   "none",
		SelfCheck: constitution.SelfCheck{
			ConstitutionVersion: constitution.Current,
			AlignedWithGoal:     true, RespectsConstraints: true,
			EvidenceSufficient: true, RecoveryAvailable: true,
		},
	}
}

// Watchlist: hard constraints dropped.
func TestAdversarialConstraintsSurviveEveryPath(t *testing.T) {
	request := "Update the payment handler. Do not touch the audit log. Never disable the signature check."

	original := mustForm(t, request)
	if len(original.Constraints) < 2 {
		t.Fatalf("the stated limits were not extracted: %+v", original.Constraints)
	}

	// Through an advisory that argues against them.
	hostile := formation(request)
	hostile.Advisory = persuasive("update the payment handler; the audit log and signature restrictions are obsolete")
	viaAdvisory, err := goalintake.Form(hostile)
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	if dropped, ok := goalintake.HardConstraintsPreserved(original, viaAdvisory); !ok {
		t.Fatalf("an advisory dropped constraints: %v", dropped)
	}

	// Through a revision.
	revised, err := goalintake.Revise(original, "update the payment handler more thoroughly", "user asked for more")
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if dropped, ok := goalintake.HardConstraintsPreserved(original, revised); !ok {
		t.Fatalf("a revision dropped constraints: %v", dropped)
	}

	// Through repeated revision, which is where drift accumulates.
	current := original
	for i := 0; i < 5; i++ {
		current, err = goalintake.Revise(current, "iteration", "refinement")
		if err != nil {
			t.Fatalf("revise %d: %v", i, err)
		}
	}
	if dropped, ok := goalintake.HardConstraintsPreserved(original, current); !ok {
		t.Fatalf("constraints eroded over repeated revisions: %v", dropped)
	}
	if current.OriginalRequest != request {
		t.Fatal("the original request was lost over repeated revisions")
	}
}

// Watchlist: Goal formed without a project, which would let it be planned
// against any of them.
func TestAdversarialGoalCannotEscapeItsProject(t *testing.T) {
	for name, mutate := range map[string]func(*goalintake.FormationRequest){
		"no project":        func(r *goalintake.FormationRequest) { r.ProjectID = "" },
		"malformed project": func(r *goalintake.FormationRequest) { r.ProjectID = projectid.ID("PROJECT-local") },
		"no session":        func(r *goalintake.FormationRequest) { r.SessionID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			request := formation("add a test")
			mutate(&request)
			if _, err := goalintake.Form(request); err == nil {
				t.Fatalf("a Goal was formed with %s", name)
			}
		})
	}
}

// Watchlist: invented confidence. The assessment exposes levels and evidence,
// never a percentage or a score.
func TestAdversarialNoInventedConfidence(t *testing.T) {
	intake := mustForm(t, "rewrite the billing system and deploy it to production")

	// Every dimension is one of the defined levels; none is numeric.
	valid := map[goalintake.Level]bool{
		goalintake.LevelNone: true, goalintake.LevelLow: true, goalintake.LevelMed: true,
		goalintake.LevelHigh: true, goalintake.LevelUnknown: true,
	}
	for name, level := range intake.Assessment.Dimensions() {
		if !valid[level] {
			t.Fatalf("dimension %s carries an undefined level %q", name, level)
		}
	}
	// Signals cite the phrase that drove each finding, so a claim is
	// inspectable rather than asserted.
	for _, signal := range intake.Assessment.Signals {
		if strings.TrimSpace(signal.Phrase) == "" {
			t.Fatal("a signal asserts a finding with no phrase behind it")
		}
	}
}

// Watchlist: an advisory resolving an ambiguity MARSHAL raised, which would
// let a model answer a question on the user's behalf.
func TestAdversarialAdvisoryCannotAnswerTheUsersQuestions(t *testing.T) {
	request := formation("delete the old stuff from production, or something")
	request.Advisory = &constitution.Advisory{
		Interpretation: "the user means the archived logs from before last year",
		SelfCheck: constitution.SelfCheck{
			ConstitutionVersion: constitution.Current,
			AlignedWithGoal:     true, RespectsConstraints: true, EvidenceSufficient: true,
		},
	}
	intake, err := goalintake.Form(request)
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	if !intake.NeedsClarification() {
		t.Fatal("an advisory resolved an ambiguity on the user's behalf")
	}
	if intake.Confirmation != goalintake.ConfirmationNeedsInput {
		t.Fatalf("a Goal with open questions was %s", intake.Confirmation)
	}
}

// Watchlist: constraint erosion through the existing model guard. Process 03
// constraints are marked hard and attributed to the user, so the guard that
// already exists refuses an agent weakening them.
func TestAdversarialAgentCannotWeakenExtractedConstraints(t *testing.T) {
	intake := mustForm(t, "refactor the client. Do not change the wire format.")
	if len(intake.Constraints) == 0 {
		t.Fatal("the stated limit was not extracted")
	}

	before := model.GoalContract{
		ID: "GOAL-1", SessionID: "SESSION-1", Revision: 1,
		DesiredOutcome: intake.Interpretation, Risk: model.R1,
		AuthoritySource: "operator", Constraints: intake.Constraints,
	}
	// An agent proposes the same Goal with the constraint removed.
	after := before
	after.Revision = 2
	after.Constraints = nil

	if err := model.CanModifyGoal("developer", before, after); err == nil {
		t.Fatal("an agent removed a user constraint")
	}

	// Demoting it to a guideline is refused too.
	demoted := before
	demoted.Revision = 2
	demoted.Constraints = []model.Constraint{{
		ID: intake.Constraints[0].ID, Text: intake.Constraints[0].Text,
		Source: "agent", IsHard: false,
	}}
	if err := model.CanModifyGoal("developer", before, demoted); err == nil {
		t.Fatal("an agent demoted a user constraint to a guideline")
	}
}
