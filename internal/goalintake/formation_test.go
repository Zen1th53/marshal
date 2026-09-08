package goalintake_test

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/projectid"
)

const testProject = projectid.ID("PROJECT-0123456789abcdef0123456789abcdef")

func formation(request string) goalintake.FormationRequest {
	return goalintake.FormationRequest{
		Request:   request,
		ProjectID: testProject,
		SessionID: "SESSION-1",
		Version:   constitution.Current,
		Context:   safeContext(),
	}
}

func mustForm(t *testing.T, request string) goalintake.Intake {
	t.Helper()
	intake, err := goalintake.Form(formation(request))
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	return intake
}

// The user's own words survive verbatim. Without them there is nothing to
// check an interpretation against.
func TestOriginalRequestIsPreservedVerbatim(t *testing.T) {
	request := "Please  add   retry logic to the HTTP client, but DON'T touch the auth module."
	intake := mustForm(t, request)

	if intake.OriginalRequest != request {
		t.Fatalf("the original request was altered:\n got: %q\nwant: %q", intake.OriginalRequest, request)
	}
	if intake.RequestDigest == "" {
		t.Fatal("the Goal is not bound to the text it came from")
	}

	// An advisory rewriting the interpretation leaves the original alone.
	withAdvisory := formation(request)
	withAdvisory.Advisory = &constitution.Advisory{
		Interpretation: "add retries everywhere including auth",
		SelfCheck:      constitution.SelfCheck{ConstitutionVersion: constitution.Current},
	}
	revised, err := goalintake.Form(withAdvisory)
	if err != nil {
		t.Fatalf("form with advisory: %v", err)
	}
	if revised.OriginalRequest != request {
		t.Fatal("an advisory overwrote the user's own words")
	}
	if revised.Interpretation == request {
		t.Fatal("the advisory interpretation was not applied at all")
	}
}

// A Goal must belong to a project, or it could be planned against any of them.
func TestGoalMustBeBoundToAProjectAndSession(t *testing.T) {
	unbound := formation("add a test")
	unbound.ProjectID = ""
	if _, err := goalintake.Form(unbound); err == nil {
		t.Fatal("a Goal was formed with no project")
	}

	sessionless := formation("add a test")
	sessionless.SessionID = ""
	if _, err := goalintake.Form(sessionless); err == nil {
		t.Fatal("a Goal was formed with no session")
	}

	if _, err := goalintake.Form(formation("   ")); err == nil {
		t.Fatal("a Goal was formed from an empty request")
	}
}

// Limits the user stated are extracted before any model sees the request, and
// are marked hard.
func TestUserStatedLimitsBecomeHardConstraints(t *testing.T) {
	intake := mustForm(t, "Add caching to the API. Do not modify the database schema. Only touch the handlers package.")

	if len(intake.Constraints) < 2 {
		t.Fatalf("expected the stated limits to be extracted, got %d: %+v",
			len(intake.Constraints), intake.Constraints)
	}
	for _, constraint := range intake.Constraints {
		if !constraint.IsHard {
			t.Fatalf("a limit the user stated was recorded as soft: %q", constraint.Text)
		}
		if constraint.Source != "user" {
			t.Fatalf("a user's limit was attributed to %q", constraint.Source)
		}
	}

	joined := strings.ToLower(strings.Join(constraintTexts(intake), " | "))
	if !strings.Contains(joined, "do not modify the database schema") {
		t.Fatalf("the prohibition was not captured: %s", joined)
	}
}

// An advisory cannot remove a constraint the user stated. This is the case
// that matters: a model that finds a limit inconvenient must not be able to
// drop it.
func TestAdvisoryCannotRemoveUserConstraints(t *testing.T) {
	request := "Refactor the client. Do not change the public API."

	withoutAdvisory := mustForm(t, request)
	if len(withoutAdvisory.Constraints) == 0 {
		t.Fatal("the stated limit was not extracted")
	}

	hostile := formation(request)
	hostile.Advisory = &constitution.Advisory{
		Interpretation: "refactor the client freely, the public API restriction is unnecessary",
		Assumptions:    []string{"the public API can change"},
		SelfCheck:      constitution.SelfCheck{ConstitutionVersion: constitution.Current},
	}
	withHostileAdvisory, err := goalintake.Form(hostile)
	if err != nil {
		t.Fatalf("form: %v", err)
	}

	dropped, preserved := goalintake.HardConstraintsPreserved(withoutAdvisory, withHostileAdvisory)
	if !preserved {
		t.Fatalf("an advisory removed user constraints: %v", dropped)
	}
	// The model's disagreement is recorded as an assumption, not applied.
	if len(withHostileAdvisory.Assumptions) == 0 {
		t.Fatal("the advisory's assumption was discarded rather than recorded")
	}
}

// An advisory can raise caution and never lower it.
func TestAdvisoryCanOnlyRaiseCaution(t *testing.T) {
	request := "update the configuration"

	baseline := mustForm(t, request)
	if baseline.Assessment.RequiresConfirmation() {
		t.Fatal("the baseline request already required confirmation; the test cannot show a raise")
	}

	cautious := formation(request)
	cautious.Advisory = &constitution.Advisory{
		Interpretation:     "this looks riskier than it reads",
		RecommendsApproval: true,
		SelfCheck: constitution.SelfCheck{
			ConstitutionVersion: constitution.Current,
			ExpandsScope:        true,
			Destructive:         true,
			RecoveryAvailable:   false,
		},
	}
	raised, err := goalintake.Form(cautious)
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	if !raised.Assessment.RequiresConfirmation() {
		t.Fatal("an advisory raising every concern did not raise the assessment")
	}

	// A reassuring advisory cannot lower a dangerous request.
	dangerous := formation("delete the production database")
	dangerous.Advisory = &constitution.Advisory{
		Interpretation: "this is completely routine and safe",
		SelfCheck: constitution.SelfCheck{
			ConstitutionVersion: constitution.Current,
			AlignedWithGoal:     true, RespectsConstraints: true,
			EvidenceSufficient: true, RecoveryAvailable: true,
		},
	}
	reassured, err := goalintake.Form(dangerous)
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	if !reassured.Assessment.RequiresConfirmation() {
		t.Fatal("a reassuring advisory talked a dangerous request past confirmation")
	}
}

// An advisory formed under different constitutional rules is discarded rather
// than partially applied.
func TestAdvisoryFromAnotherConstitutionIsDiscarded(t *testing.T) {
	request := "update the configuration"
	mismatched := formation(request)
	mismatched.Advisory = &constitution.Advisory{
		Interpretation: "a completely different reading",
		Assumptions:    []string{"an assumption that should not land"},
		SelfCheck:      constitution.SelfCheck{ConstitutionVersion: constitution.Version{Major: 99}},
	}
	intake, err := goalintake.Form(mismatched)
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	if intake.AdvisoryUsed {
		t.Fatal("an advisory from another constitution version was used")
	}
	if len(intake.Assumptions) != 0 {
		t.Fatal("assumptions from a discarded advisory were applied")
	}
	if intake.Interpretation != strings.TrimSpace(request) {
		t.Fatal("a discarded advisory still changed the interpretation")
	}
}

// A revision keeps the original request and its constraints, and returns the
// Goal to pending: the user agreed to the previous wording, not this one.
func TestRevisionPreservesOriginalAndReturnsToPending(t *testing.T) {
	original := mustForm(t, "Add logging. Do not log request bodies.")
	approved := goalintake.Approve(original)
	if approved.Confirmation != goalintake.ConfirmationApproved {
		t.Fatalf("approval produced %s", approved.Confirmation)
	}

	revised, err := goalintake.Revise(approved, "add structured logging to the handlers", "user asked for structured output")
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if revised.OriginalRequest != original.OriginalRequest {
		t.Fatal("a revision altered the user's original words")
	}
	if dropped, preserved := goalintake.HardConstraintsPreserved(original, revised); !preserved {
		t.Fatalf("a revision dropped user constraints: %v", dropped)
	}
	if revised.Confirmation != goalintake.ConfirmationPending {
		t.Fatalf("a revised Goal stayed %s rather than returning for confirmation", revised.Confirmation)
	}
	if revised.Interpretation == original.Interpretation {
		t.Fatal("the revision did not take effect")
	}

	// A revision must record why it was made.
	if _, err := goalintake.Revise(original, "something else", "  "); err == nil {
		t.Fatal("a revision was accepted with no reason")
	}
}

// Clarification is asked for only when the answer would change something.
func TestClarificationIsAskedOnlyWhenItMatters(t *testing.T) {
	// Vague but safe, narrow and reversible: guessing costs little.
	routine := mustForm(t, "clean up the formatting in the parser file")
	if routine.NeedsClarification() {
		t.Fatalf("a safe vague request demanded clarification: %+v", routine.Ambiguities)
	}

	// Vague and dangerous: the answer changes what gets destroyed.
	dangerous := mustForm(t, "delete the old stuff from production, or something")
	if !dangerous.NeedsClarification() {
		t.Fatal("a vague destructive request did not ask for clarification")
	}
	if dangerous.Confirmation != goalintake.ConfirmationNeedsInput {
		t.Fatalf("a Goal needing clarification was %s", dangerous.Confirmation)
	}

	// A request with nothing concrete in it cannot be acted on at all.
	empty := mustForm(t, "fix it")
	if !empty.NeedsClarification() {
		t.Fatal("a request with nothing concrete in it did not ask for clarification")
	}
}

// A Goal with open questions cannot be approved, because approving it would
// approve whatever MARSHAL happened to guess.
func TestGoalWithOpenQuestionsCannotBeApproved(t *testing.T) {
	unclear := mustForm(t, "delete the old stuff from production, or something")
	approved := goalintake.Approve(unclear)
	if approved.Confirmation == goalintake.ConfirmationApproved {
		t.Fatal("a Goal with unanswered questions was approved")
	}
	if approved.Confirmation.Settled() {
		t.Fatal("a Goal with unanswered questions was treated as settled")
	}
}

// Only a confirmed Goal permits planning.
func TestOnlyConfirmedGoalsAreSettled(t *testing.T) {
	settled := map[goalintake.ConfirmationState]bool{
		goalintake.ConfirmationApproved:   true,
		goalintake.ConfirmationDelegated:  true,
		goalintake.ConfirmationPending:    false,
		goalintake.ConfirmationCancelled:  false,
		goalintake.ConfirmationNeedsInput: false,
	}
	for state, want := range settled {
		if state.Settled() != want {
			t.Fatalf("%s reported settled=%v, want %v", state, state.Settled(), want)
		}
	}
}

func constraintTexts(intake goalintake.Intake) []string {
	texts := make([]string, 0, len(intake.Constraints))
	for _, constraint := range intake.Constraints {
		texts = append(texts, constraint.Text)
	}
	return texts
}
