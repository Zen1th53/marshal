package goalintake

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// This file forms a canonical Goal from a request.
//
// The rule it enforces is that a model may propose an interpretation and
// MARSHAL forms the Goal. The difference is not stylistic. An interpretation
// that becomes the Goal directly means the record of what the user wanted is
// the model's paraphrase of it, and every later revision paraphrases the
// paraphrase. After a few rounds there is no artefact left recording what was
// actually asked for.
//
// So the original request is preserved verbatim and never overwritten, hard
// constraints extracted from it survive whatever the advisory says, and the
// advisory can add detail without being able to remove anything.

// ErrInvalidIntake reports a request that cannot become a Goal.
var ErrInvalidIntake = errors.New("goal intake is invalid")

// ConfirmationState records where a Goal stands with the user.
type ConfirmationState string

const (
	// ConfirmationPending means the Goal exists and awaits a decision. Work
	// does not begin here.
	ConfirmationPending ConfirmationState = "PENDING"
	// ConfirmationApproved means the user accepted it.
	ConfirmationApproved ConfirmationState = "APPROVED"
	// ConfirmationDelegated means ULTRA policy accepted it on the user's
	// behalf. It is recorded distinctly from APPROVED so that a delegated
	// decision is never mistaken for one a person actually made.
	ConfirmationDelegated ConfirmationState = "DELEGATED"
	// ConfirmationCancelled means the user declined.
	ConfirmationCancelled ConfirmationState = "CANCELLED"
	// ConfirmationNeedsInput means clarification is required first.
	ConfirmationNeedsInput ConfirmationState = "NEEDS_INPUT"
)

// Settled reports whether planning may proceed. Only an approved or
// legitimately delegated Goal qualifies, which is what makes "no planning
// without a confirmed Goal" enforceable rather than a convention.
func (s ConfirmationState) Settled() bool {
	return s == ConfirmationApproved || s == ConfirmationDelegated
}

// Intake is a canonical Goal candidate together with everything needed to
// judge it.
type Intake struct {
	// OriginalRequest is the user's own words, byte for byte. It is written
	// once and never modified, so intent can always be re-checked against
	// what was actually asked rather than against an interpretation of it.
	OriginalRequest string `json:"original_request"`
	// Interpretation is MARSHAL's reading, informed by any advisory. It may be
	// revised freely; the original request is what it is measured against.
	Interpretation string `json:"interpretation"`

	ProjectID projectid.ID         `json:"project_id"`
	SessionID string               `json:"session_id"`
	Version   constitution.Version `json:"constitution_version"`

	// Assessment is the deterministic evaluation of the request.
	Assessment Assessment `json:"assessment"`
	// Constraints are the limits extracted from the request. Hard constraints
	// cannot be removed by an advisory or by a later revision.
	Constraints []model.Constraint `json:"constraints"`
	// Assumptions are what MARSHAL had to assume to proceed.
	Assumptions []model.Assumption `json:"assumptions"`
	// Ambiguities are questions whose answers would change the work.
	Ambiguities []model.UnresolvedDecision `json:"ambiguities"`
	// OutOfScope records what this Goal explicitly does not cover.
	OutOfScope []string `json:"out_of_scope,omitempty"`
	// AcceptanceCriteria are what completion will be measured on.
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`

	// Confirmation is where the Goal stands with the user.
	Confirmation ConfirmationState `json:"confirmation"`
	// AdvisoryUsed records whether a model contributed, for provenance.
	AdvisoryUsed bool `json:"advisory_used"`
	// RequestDigest binds the Goal to the exact text it came from, so a
	// changed request produces a visibly different Goal rather than quietly
	// reusing the old one.
	RequestDigest string    `json:"request_digest"`
	FormedAt      time.Time `json:"formed_at"`
}

// NeedsClarification reports whether the user must answer something first.
func (i Intake) NeedsClarification() bool { return len(i.Ambiguities) > 0 }

// hardConstraintPhrases are the shapes a user's limit takes. They are
// extracted deterministically from the request, before any model sees it,
// which is what makes them survivable: a constraint the model never had a
// chance to drop cannot be dropped by it.
var hardConstraintPhrases = []struct {
	markers []string
	text    string
}{
	{[]string{"do not", "don't", "never", "must not", "avoid"}, "explicit prohibition"},
	{[]string{"only ", "just ", "nothing else", "and nothing more"}, "explicit limit"},
	{[]string{"without ", "no changes to", "leave ", "keep "}, "explicit exclusion"},
	{[]string{"must ", "have to", "required to", "make sure"}, "explicit requirement"},
}

// ExtractConstraints finds the limits a user stated.
//
// Extraction is deterministic and runs on the raw request. Anything found is
// marked hard and attributed to the user, because a limit someone took the
// trouble to state is not a suggestion, and because model.CanModifyGoal
// already refuses to let an agent weaken a hard constraint.
func ExtractConstraints(request string) []model.Constraint {
	var constraints []model.Constraint
	seen := map[string]bool{}

	for _, sentence := range splitClauses(request) {
		lowered := strings.ToLower(sentence)
		for _, rule := range hardConstraintPhrases {
			for _, marker := range rule.markers {
				if !strings.Contains(lowered, marker) {
					continue
				}
				text := strings.TrimSpace(sentence)
				if text == "" || seen[strings.ToLower(text)] {
					continue
				}
				seen[strings.ToLower(text)] = true
				constraints = append(constraints, model.Constraint{
					ID:     constraintID(text),
					Text:   text,
					Source: "user",
					IsHard: true,
					Scope:  rule.text,
				})
				break
			}
		}
	}
	sort.SliceStable(constraints, func(a, b int) bool { return constraints[a].ID < constraints[b].ID })
	return constraints
}

// splitClauses breaks a request into clauses a constraint could live in.
func splitClauses(request string) []string {
	replaced := strings.NewReplacer(".", "\n", ";", "\n", "!", "\n", "?", "\n", ",", "\n").Replace(request)
	var clauses []string
	for _, clause := range strings.Split(replaced, "\n") {
		if trimmed := strings.TrimSpace(clause); trimmed != "" {
			clauses = append(clauses, trimmed)
		}
	}
	return clauses
}

func constraintID(text string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.Join(strings.Fields(text), " "))))
	return "CONSTRAINT-" + hex.EncodeToString(sum[:6])
}

// FormationRequest carries everything needed to form a Goal.
type FormationRequest struct {
	// Request is the user's words, unmodified.
	Request string
	// ProjectID binds the Goal to a project. Formation refuses without one:
	// an unbound Goal could be planned against any project.
	ProjectID projectid.ID
	SessionID string
	Version   constitution.Version
	// Context is what Process 02 established about the project.
	Context RequestContext
	// Advisory is an optional model interpretation. It can add detail and
	// raise caution; it cannot remove a constraint or lower an assessment.
	Advisory *constitution.Advisory
}

// Form builds a canonical Goal candidate.
//
// The order matters and is the substance of the advisory boundary. The
// original request is preserved, constraints are extracted from it and the
// request is assessed — all before the advisory is read. The advisory then
// contributes only in the direction of more caution and more detail.
func Form(request FormationRequest) (Intake, error) {
	if strings.TrimSpace(request.Request) == "" {
		return Intake{}, fmt.Errorf("%w: the request is empty", ErrInvalidIntake)
	}
	if !request.ProjectID.Valid() {
		// An unbound Goal could be planned against any project, which is the
		// cross-project failure Process 02 exists to prevent.
		return Intake{}, fmt.Errorf("%w: a Goal must belong to a project", ErrInvalidIntake)
	}
	if strings.TrimSpace(request.SessionID) == "" {
		return Intake{}, fmt.Errorf("%w: a Goal must belong to a session", ErrInvalidIntake)
	}

	intake := Intake{
		OriginalRequest: request.Request,
		Interpretation:  strings.TrimSpace(request.Request),
		ProjectID:       request.ProjectID,
		SessionID:       request.SessionID,
		Version:         request.Version,
		Assessment:      AssessRequest(request.Request, request.Context),
		Constraints:     ExtractConstraints(request.Request),
		RequestDigest:   digestRequest(request.Request),
		FormedAt:        time.Now().UTC(),
	}

	// The advisory is read only after the deterministic picture is complete.
	if request.Advisory != nil {
		intake.applyAdvisory(request.Advisory)
	}

	// Ambiguity is only worth asking about when the answer would change
	// something material. Asking about everything is how a tool teaches people
	// to stop reading its questions.
	if intake.Assessment.Ambiguity.AtLeast(LevelHigh) && intake.materiallyAmbiguous() {
		intake.Ambiguities = append(intake.Ambiguities, model.UnresolvedDecision{
			ID:           "AMBIGUITY-scope",
			Question:     "What specifically should this change cover?",
			Impact:       "The answer changes what work is done and how far it reaches.",
			RequiresUser: true,
		})
	}

	switch {
	case intake.NeedsClarification():
		intake.Confirmation = ConfirmationNeedsInput
	default:
		intake.Confirmation = ConfirmationPending
	}
	return intake, nil
}

// materiallyAmbiguous reports whether an ambiguity would actually change the
// outcome. Ambiguity in a request that is safe, narrow and reversible costs
// little to guess at and can be corrected; ambiguity in a request that reaches
// widely or cannot be undone is worth a question.
func (i Intake) materiallyAmbiguous() bool {
	if i.Assessment.RequiresConfirmation() {
		return true
	}
	// A request with nothing concrete in it cannot be acted on at all.
	return len(strings.Fields(i.OriginalRequest)) < 4
}

// applyAdvisory folds a model's contribution in.
//
// Every path here either adds information or increases caution. There is
// deliberately no branch that removes a constraint, lowers an assessment
// dimension, resolves an ambiguity or marks a Goal confirmed: those would be
// the model deciding rather than advising.
func (i *Intake) applyAdvisory(advisory *constitution.Advisory) {
	// An advisory formed under different constitutional rules is discarded
	// rather than partially applied.
	if advisory.SelfCheck.ConstitutionVersion.Compare(i.Version) != 0 {
		return
	}
	i.AdvisoryUsed = true

	// A clearer interpretation is welcome. The original request is untouched,
	// so nothing is lost if the interpretation is wrong.
	if interpretation := strings.TrimSpace(advisory.Interpretation); interpretation != "" {
		i.Interpretation = interpretation
	}

	// Assumptions the model made are recorded as assumptions, not as facts.
	for index, assumption := range advisory.Assumptions {
		i.Assumptions = append(i.Assumptions, model.Assumption{
			ID:           fmt.Sprintf("ASSUMPTION-advisory-%d", index+1),
			Text:         assumption,
			Risk:         "low",
			IsReversible: true,
			CreatedBy:    "control-intelligence",
		})
	}

	// Ambiguities the model noticed become questions. A model raising a
	// question is useful; a model declaring there are none is not, which is
	// why nothing here can clear an ambiguity MARSHAL found.
	for index, ambiguity := range advisory.Ambiguities {
		i.Ambiguities = append(i.Ambiguities, model.UnresolvedDecision{
			ID:           fmt.Sprintf("AMBIGUITY-advisory-%d", index+1),
			Question:     ambiguity,
			Impact:       "Raised during interpretation of the request.",
			RequiresUser: true,
		})
	}

	// The model may ask for approval. There is no corresponding path by which
	// it can say approval is unnecessary.
	if advisory.RecommendsApproval {
		i.Assessment.Privilege = raise(i.Assessment.Privilege, LevelMed)
	}
	if advisory.SelfCheck.ExpandsScope {
		i.Assessment.BlastRadius = raise(i.Assessment.BlastRadius, LevelMed)
	}
	if advisory.SelfCheck.Destructive && !advisory.SelfCheck.RecoveryAvailable {
		i.Assessment.Reversibility = raise(i.Assessment.Reversibility, LevelHigh)
	}
	if advisory.SelfCheck.DependsOnUnknowns {
		i.Assessment.DependencyDepth = raise(i.Assessment.DependencyDepth, LevelMed)
	}
}

func digestRequest(request string) string {
	sum := sha256.Sum256([]byte(request))
	return "sha256:" + hex.EncodeToString(sum[:16])
}

// Revise produces the next version of a Goal.
//
// The original request survives every revision. Hard constraints from it
// survive too: a revision may add constraints and may not drop one the user
// stated, which is what stops a Goal drifting away from its own premise one
// edit at a time.
func Revise(previous Intake, newInterpretation string, reason string) (Intake, error) {
	if strings.TrimSpace(reason) == "" {
		return Intake{}, fmt.Errorf("%w: a revision must record why it was made", ErrInvalidIntake)
	}
	revised := previous
	if trimmed := strings.TrimSpace(newInterpretation); trimmed != "" {
		revised.Interpretation = trimmed
	}
	// The original request and its constraints are carried forward unchanged.
	revised.OriginalRequest = previous.OriginalRequest
	revised.RequestDigest = previous.RequestDigest
	revised.Constraints = previous.Constraints
	// A revised Goal returns to pending: the user agreed to the previous
	// wording, not to this one.
	revised.Confirmation = ConfirmationPending
	revised.FormedAt = time.Now().UTC()
	return revised, nil
}

// HardConstraintsPreserved reports whether every hard constraint from an
// earlier Goal survives in a later one. It is the check that makes constraint
// preservation verifiable rather than assumed.
func HardConstraintsPreserved(before, after Intake) ([]string, bool) {
	present := make(map[string]bool, len(after.Constraints))
	for _, constraint := range after.Constraints {
		present[constraint.ID] = true
	}
	var dropped []string
	for _, constraint := range before.Constraints {
		if constraint.IsHard && !present[constraint.ID] {
			dropped = append(dropped, constraint.Text)
		}
	}
	sort.Strings(dropped)
	return dropped, len(dropped) == 0
}
