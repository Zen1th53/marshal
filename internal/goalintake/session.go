package goalintake

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// This file defines the canonical session and what survives a provider
// changing underneath it.
//
// The rule is that provider sessions are disposable and the MARSHAL session is
// canonical. That is not a preference about where state lives; it is what
// makes failover possible at all. If a provider's own conversation were the
// record of what the user wanted, losing that provider would lose the work,
// and switching providers mid-task would mean starting again with whatever the
// new one could be told in a single message.
//
// So everything needed to continue is held here, in MARSHAL's own terms, and a
// continuation package can be rebuilt for any provider from it.

// ErrSessionInvalid reports a session that cannot be continued.
var ErrSessionInvalid = errors.New("session is invalid")

// Session is MARSHAL's canonical record of a piece of work.
//
// It deliberately holds no provider conversation, no message history and no
// provider session identifier beyond a disposable handle. Everything here is
// something MARSHAL established and can restate.
type Session struct {
	ID        string       `json:"id"`
	ProjectID projectid.ID `json:"project_id"`
	// Version is the constitution the session is bound to. It is fixed at
	// creation so a runtime upgrade cannot silently reinterpret work already
	// done (Process 00, Article XXII).
	Version constitution.Version `json:"constitution_version"`
	// Goal is the canonical Goal. The user's original request lives here, so
	// intent survives any provider change.
	Goal model.GoalContract `json:"goal"`
	// Mode is Standard or ULTRA.
	Mode Mode `json:"mode"`

	// Provider is the currently selected provider. It is a choice, not a
	// dependency: losing it costs a reselection, not the session.
	Provider string `json:"provider,omitempty"`
	// ProviderSessionID is the provider's own handle, kept only so a live
	// conversation can be reused while it lasts. It is explicitly disposable
	// and nothing is reconstructed from it.
	ProviderSessionID string `json:"provider_session_id,omitempty"`

	// Failovers records provider changes, so a user can see that work moved
	// rather than finding it silently continued elsewhere.
	Failovers []Failover `json:"failovers,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Failover records one provider change and why it happened.
type Failover struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Reason is user-safe.
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// Validate rejects a session that could not be continued safely.
func (s Session) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("%w: a session needs an identifier", ErrSessionInvalid)
	}
	if !s.ProjectID.Valid() {
		// A session with no project could be continued against any project,
		// which is the cross-project failure Process 02 prevents.
		return fmt.Errorf("%w: a session must belong to a project", ErrSessionInvalid)
	}
	if s.Version.IsZero() {
		return fmt.Errorf("%w: a session must be bound to a constitution version", ErrSessionInvalid)
	}
	if strings.TrimSpace(s.Goal.OriginalRequest) == "" {
		// Without the original request a continued session has no way to
		// check itself against what was actually asked.
		return fmt.Errorf("%w: a session must carry the original request", ErrSessionInvalid)
	}
	if s.Mode != ModeStandard && s.Mode != ModeUltra {
		return fmt.Errorf("%w: unknown session mode %q", ErrSessionInvalid, s.Mode)
	}
	return nil
}

// Continuation is everything a provider needs to pick up work, expressed in
// MARSHAL's terms rather than any provider's.
//
// It is rebuilt from the session on demand rather than stored, so it cannot
// drift from the session it describes, and so a new provider gets exactly what
// the previous one had rather than an accumulated transcript.
type Continuation struct {
	// OriginalRequest is what the user actually asked, verbatim. It leads
	// because everything else is derived from it.
	OriginalRequest string `json:"original_request"`
	// Interpretation is MARSHAL's current reading.
	Interpretation string `json:"interpretation"`
	// HardConstraints are the limits that must be restated to every provider.
	// Re-injecting them on each handoff is what stops them being lost when a
	// provider changes — a constraint held only in a previous conversation is
	// a constraint that ends with it.
	HardConstraints []string `json:"hard_constraints"`
	// OpenQuestions are ambiguities still awaiting an answer.
	OpenQuestions []string `json:"open_questions,omitempty"`
	// Confirmation is where the Goal stands, so a provider cannot be handed
	// work the user has not agreed to.
	Confirmation model.ConfirmationState `json:"confirmation"`
	// ProjectID and Version bind the continuation to one project and one set
	// of rules.
	ProjectID projectid.ID         `json:"project_id"`
	Version   constitution.Version `json:"constitution_version"`
}

// BuildContinuation assembles what a provider needs to continue.
//
// It is deliberately small. A provider is given the request, the current
// reading, the constraints and the open questions — not a transcript, not
// prior model output, and nothing another provider said. Carrying a
// conversation forward would make each handoff compound the last one's
// mistakes, and would make continuation depend on hidden reasoning the
// constitution treats as non-canonical.
func BuildContinuation(session Session) (Continuation, error) {
	if err := session.Validate(); err != nil {
		return Continuation{}, err
	}

	continuation := Continuation{
		OriginalRequest: session.Goal.OriginalRequest,
		Interpretation:  session.Goal.DesiredOutcome,
		Confirmation:    session.Goal.Confirmation,
		ProjectID:       session.ProjectID,
		Version:         session.Version,
	}
	for _, constraint := range session.Goal.Constraints {
		if constraint.IsHard {
			continuation.HardConstraints = append(continuation.HardConstraints, constraint.Text)
		}
	}
	// Do-not-do entries are constraints in every sense that matters here.
	continuation.HardConstraints = append(continuation.HardConstraints, session.Goal.DoNotDo...)
	sort.Strings(continuation.HardConstraints)

	for _, decision := range session.Goal.UnresolvedDecisions {
		continuation.OpenQuestions = append(continuation.OpenQuestions, decision.Question)
	}
	sort.Strings(continuation.OpenQuestions)
	return continuation, nil
}

// Failover moves a session to a different provider.
//
// The session is unchanged except for the provider and the record of the move.
// Nothing about the Goal, the constraints or the confirmation is touched,
// which is the property that makes failover safe: a provider change cannot
// alter what the user asked for or what they agreed to.
//
// The previous provider's session handle is discarded rather than carried
// over. It refers to a conversation the new provider cannot see, and keeping
// it would invite treating it as state.
func (s Session) Failover(to string, reason string, now time.Time) (Session, error) {
	if strings.TrimSpace(to) == "" {
		return s, fmt.Errorf("%w: failover needs a destination provider", ErrSessionInvalid)
	}
	if to == s.Provider {
		return s, fmt.Errorf("%w: failover destination is the current provider", ErrSessionInvalid)
	}
	if strings.TrimSpace(reason) == "" {
		// A provider change the user cannot account for is one they will
		// discover later and mistrust.
		return s, fmt.Errorf("%w: failover must record why it happened", ErrSessionInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	moved := s
	moved.Failovers = append(append([]Failover(nil), s.Failovers...), Failover{
		From: s.Provider, To: to, Reason: reason, At: now,
	})
	moved.Provider = to
	moved.ProviderSessionID = ""
	moved.UpdatedAt = now
	return moved, nil
}

// FailoverSummary describes provider changes for a user, so a session that
// moved does not appear to have run continuously somewhere it did not.
func (s Session) FailoverSummary() string {
	if len(s.Failovers) == 0 {
		return "This work has run on one provider throughout."
	}
	var parts []string
	for _, failover := range s.Failovers {
		from := failover.From
		if from == "" {
			from = "the initial provider"
		}
		parts = append(parts, fmt.Sprintf("moved from %s to %s (%s)", from, failover.To, failover.Reason))
	}
	return "This work " + strings.Join(parts, "; then ") + "."
}
