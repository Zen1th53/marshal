package belief

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrBeliefSetNotFound = errors.New("belief set not found")
	ErrHypothesisNotFound = errors.New("hypothesis not found in belief set")
)

type Hypothesis struct {
	ID                   string   `json:"id"`
	Description          string   `json:"description"`
	AgentClaim           float64  `json:"agent_claim"` // Raw unverified model assertion
	Probability          float64  `json:"probability"`  // System-derived probability
	SupportingEvidenceIDs []string `json:"supporting_evidence_ids"`
}

type BeliefSet struct {
	ObservationID       string       `json:"observation_id"`
	Description         string       `json:"description"`
	Hypotheses          []Hypothesis `json:"hypotheses"`
	Resolved            bool         `json:"resolved"`
	ResolvedWinnerID    string       `json:"resolved_winner_id,omitempty"`
	DecisiveEvidenceID  string       `json:"decisive_evidence_id,omitempty"`
	PriorAlternatives   []Hypothesis `json:"prior_alternatives,omitempty"`
	UpdatedAt           time.Time    `json:"updated_at"`
}

type Engine struct {
	mu   sync.RWMutex
	sets map[string]BeliefSet
}

func NewEngine() *Engine {
	return &Engine{
		sets: make(map[string]BeliefSet),
	}
}

// CreateBeliefSet initializes a multi-hypothesis belief set with equal initial probabilities.
func (e *Engine) CreateBeliefSet(ctx context.Context, obsID, desc string, hypotheses []Hypothesis) (BeliefSet, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	n := len(hypotheses)
	if n == 0 {
		return BeliefSet{}, errors.New("must have at least one hypothesis")
	}

	initProb := 1.0 / float64(n)
	for i := range hypotheses {
		hypotheses[i].Probability = initProb
	}

	set := BeliefSet{
		ObservationID: obsID,
		Description:   desc,
		Hypotheses:    hypotheses,
		UpdatedAt:     time.Now().UTC(),
	}
	e.sets[obsID] = set
	return set, nil
}

// CalculateDerivedProbability computes bounded probability from empirical evidence counts rather than raw agent claims.
func (e *Engine) CalculateDerivedProbability(h Hypothesis, totalHypotheses int) float64 {
	// Laplace smoothed evidence weight
	evCount := float64(len(h.SupportingEvidenceIDs))
	return (1.0 + evCount) / (float64(totalHypotheses) + evCount + 1.0)
}

// AddEvidence links evidence to a specific hypothesis and recalculates probability distribution.
func (e *Engine) AddEvidence(ctx context.Context, obsID, hypothesisID, evidenceID string) (BeliefSet, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	set, ok := e.sets[obsID]
	if !ok {
		return BeliefSet{}, ErrBeliefSetNotFound
	}

	found := false
	for i := range set.Hypotheses {
		if set.Hypotheses[i].ID == hypothesisID {
			set.Hypotheses[i].SupportingEvidenceIDs = append(set.Hypotheses[i].SupportingEvidenceIDs, evidenceID)
			found = true
			break
		}
	}
	if !found {
		return BeliefSet{}, ErrHypothesisNotFound
	}

	// Recalculate normalized probabilities
	var totalWeight float64
	weights := make([]float64, len(set.Hypotheses))
	for i, h := range set.Hypotheses {
		w := 1.0 + float64(len(h.SupportingEvidenceIDs))*2.0
		weights[i] = w
		totalWeight += w
	}
	for i := range set.Hypotheses {
		set.Hypotheses[i].Probability = weights[i] / totalWeight
	}
	set.UpdatedAt = time.Now().UTC()
	e.sets[obsID] = set

	return set, nil
}

// ResolveBelief finalizes a belief set when decisive evidence confirms a winner.
func (e *Engine) ResolveBelief(ctx context.Context, obsID, winnerHypothesisID, decisiveEvidenceID string) (BeliefSet, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	set, ok := e.sets[obsID]
	if !ok {
		return BeliefSet{}, ErrBeliefSetNotFound
	}

	var alternatives []Hypothesis
	winnerFound := false
	for _, h := range set.Hypotheses {
		if h.ID == winnerHypothesisID {
			winnerFound = true
		} else {
			alternatives = append(alternatives, h)
		}
	}
	if !winnerFound {
		return BeliefSet{}, ErrHypothesisNotFound
	}

	set.Resolved = true
	set.ResolvedWinnerID = winnerHypothesisID
	set.DecisiveEvidenceID = decisiveEvidenceID
	set.PriorAlternatives = alternatives
	set.UpdatedAt = time.Now().UTC()
	e.sets[obsID] = set

	return set, nil
}
