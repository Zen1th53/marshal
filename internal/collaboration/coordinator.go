package collaboration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

var (
	ErrSessionNotFound     = errors.New("collaboration session not found")
	ErrInvalidTurnSpeaker  = errors.New("agent is not the designated speaker for the active turn")
	ErrParticipantNotFound = errors.New("agent is not a registered participant in this session")
	ErrLoopDetected        = errors.New("unproductive collaboration loop detected; turn blocked")
	ErrSessionInactive     = errors.New("collaboration session is not active")
)

// TeamSessionOverview provides complete discovery of peer work from durable state.
type TeamSessionOverview struct {
	Session      model.TeamSession    `json:"session"`
	RecentTurns  []model.AgentMessage `json:"recent_turns"`
	ActiveClaims []model.Claim        `json:"active_claims,omitempty"`
}

// Coordinator governs multi-agent collaborative execution around a single GoalContract.
type Coordinator struct {
	mu           sync.RWMutex
	store        *store.Store
	loopDetector *LoopDetector
}

func NewCoordinator(st *store.Store, ld *LoopDetector) *Coordinator {
	if ld == nil {
		ld = NewLoopDetector()
	}
	return &Coordinator{
		store:        st,
		loopDetector: ld,
	}
}

// CreateSession establishes a new multi-agent team session with fixed roles.
func (c *Coordinator) CreateSession(
	ctx context.Context,
	sessionID, goalID string,
	revision int64,
	participants []model.Participant,
) (*model.TeamSession, error) {
	now := time.Now().UTC()

	if len(participants) == 0 {
		return nil, fmt.Errorf("%w: at least one participant required", model.ErrInvalid)
	}

	firstAgent := participants[0].AgentID

	sess := model.TeamSession{
		SessionID:    sessionID,
		GoalID:       goalID,
		GoalRevision: revision,
		Participants: participants,
		ActiveTurn:   firstAgent,
		TurnSequence: 1,
		Status:       "ACTIVE",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := sess.Validate(); err != nil {
		return nil, err
	}

	if c.store != nil {
		if err := c.store.SaveTeamSession(ctx, sess); err != nil {
			return nil, fmt.Errorf("save team session: %w", err)
		}
	}

	return &sess, nil
}

// StartTurn assigns writer/speaker ownership for the next collaborative turn.
func (c *Coordinator) StartTurn(ctx context.Context, sessionID, agentID string) (*model.TeamSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	sess, err := c.store.GetTeamSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionNotFound, err)
	}

	if sess.Status != "ACTIVE" {
		return nil, fmt.Errorf("%w: current status %s", ErrSessionInactive, sess.Status)
	}

	// Verify agent is a valid participant
	isParticipant := false
	for _, p := range sess.Participants {
		if p.AgentID == agentID && p.IsActive {
			isParticipant = true
			break
		}
	}
	if !isParticipant {
		return nil, fmt.Errorf("%w: agent %s", ErrParticipantNotFound, agentID)
	}

	sess.ActiveTurn = agentID
	sess.TurnSequence++
	sess.UpdatedAt = time.Now().UTC()

	if err := c.store.SaveTeamSession(ctx, *sess); err != nil {
		return nil, fmt.Errorf("save active turn: %w", err)
	}

	return sess, nil
}

// SendMessage persists typed communication and evaluates loop protection.
func (c *Coordinator) SendMessage(ctx context.Context, msg model.AgentMessage, hasNewEvidence, hasNewDiff bool) (*model.LoopDetectionResult, error) {
	if err := msg.Validate(); err != nil {
		return nil, err
	}

	sess, err := c.store.GetTeamSession(ctx, msg.SessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionNotFound, err)
	}

	// Fetch recent messages for loop analysis
	history, err := c.store.ListAgentMessages(ctx, msg.SessionID, 50)
	if err != nil {
		return nil, fmt.Errorf("list message history: %w", err)
	}

	// Check loop protection with the prospective message
	evalHistory := append(history, msg)
	loopRes := c.loopDetector.Evaluate(evalHistory, sess.TurnSequence, hasNewEvidence, hasNewDiff)

	if loopRes.LoopDetected {
		// Persist the message anyway for forensic audit, but pause the session
		_ = c.store.SaveAgentMessage(ctx, msg)
		sess.Status = "PAUSED"
		sess.UpdatedAt = time.Now().UTC()
		_ = c.store.SaveTeamSession(ctx, *sess)
		return &loopRes, fmt.Errorf("%w: %s (%s)", ErrLoopDetected, loopRes.Reason, loopRes.LoopKind)
	}

	// Save message
	if err := c.store.SaveAgentMessage(ctx, msg); err != nil {
		return nil, fmt.Errorf("save agent message: %w", err)
	}

	return &loopRes, nil
}

// ChallengeClaim records counter-evidence, transitions claim state to CONTESTED, and broadcasts challenge.
func (c *Coordinator) ChallengeClaim(
	ctx context.Context,
	sessionID string,
	challenger model.AuthorProvenance,
	claimID string,
	counterEvidence model.EvidenceRef,
	reason string,
) error {
	now := time.Now().UTC()

	claim, err := c.store.GetClaim(ctx, claimID)
	if err != nil {
		return fmt.Errorf("retrieve claim %s: %w", claimID, err)
	}

	// Update claim state to CONTESTED
	transition := model.ClaimTransition{
		TransitionID: fmt.Sprintf("tr-ch-%d-%s", now.UnixNano(), claimID),
		ClaimID:      claimID,
		FromState:    claim.State,
		ToState:      model.ClaimStateContested,
		Reason:       reason,
		Actor:        challenger,
		EvidenceRef:  &counterEvidence,
		Timestamp:    now,
	}

	claim.State = model.ClaimStateContested
	claim.StateReason = reason
	claim.ContradictingEvidence = append(claim.ContradictingEvidence, counterEvidence)
	claim.EvaluatedAt = now

	if err := c.store.SaveClaim(ctx, claim); err != nil {
		return fmt.Errorf("update claim: %w", err)
	}
	if err := c.store.RecordClaimTransition(ctx, transition); err != nil {
		return fmt.Errorf("record transition: %w", err)
	}

	// Emit typed message to team
	msg := model.AgentMessage{
		ID:          fmt.Sprintf("msg-ch-%d", now.UnixNano()),
		SessionID:   sessionID,
		From:        challenger,
		Kind:        model.MessageClaimChallenge,
		Content:     fmt.Sprintf("Claim %s challenged: %s", claimID, reason),
		ClaimIDs:    []string{claimID},
		EvidenceIDs: []string{counterEvidence.EvidenceID},
		CreatedAt:   now,
	}

	_, _ = c.SendMessage(ctx, msg, true, false)
	return nil
}

// HandOffOwnership transfers active turn to the assigned peer role with durable record.
func (c *Coordinator) HandOffOwnership(
	ctx context.Context,
	sessionID string,
	fromAgent model.AuthorProvenance,
	targetRole model.Role,
	handoffSummary string,
	evidenceIDs []string,
	claimIDs []string,
) (*model.TeamSession, error) {
	now := time.Now().UTC()

	sess, err := c.store.GetTeamSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionNotFound, err)
	}

	// Find active participant for targetRole
	var targetAgent string
	for _, p := range sess.Participants {
		if p.Role == targetRole && p.IsActive {
			targetAgent = p.AgentID
			break
		}
	}
	if targetAgent == "" {
		return nil, fmt.Errorf("no active participant registered for target role %q", targetRole)
	}

	// Post HANDOFF_PROPOSAL message
	msg := model.AgentMessage{
		ID:          fmt.Sprintf("msg-ho-%d", now.UnixNano()),
		SessionID:   sessionID,
		From:        fromAgent,
		To:          targetAgent,
		Kind:        model.MessageHandoffProposal,
		Content:     handoffSummary,
		ClaimIDs:    claimIDs,
		EvidenceIDs: evidenceIDs,
		CreatedAt:   now,
	}

	if _, err := c.SendMessage(ctx, msg, len(evidenceIDs) > 0, false); err != nil {
		return nil, err
	}

	// Advance turn to targetAgent
	sess.ActiveTurn = targetAgent
	sess.TurnSequence++
	sess.UpdatedAt = now

	if err := c.store.SaveTeamSession(ctx, *sess); err != nil {
		return nil, fmt.Errorf("save handoff turn: %w", err)
	}

	return sess, nil
}

// GetSessionOverview provides complete discovery of peer work from durable state.
// Agents resuming after restart use this to catch up on findings and claims without private transcript replay.
func (c *Coordinator) GetSessionOverview(ctx context.Context, sessionID string) (*TeamSessionOverview, error) {
	sess, err := c.store.GetTeamSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionNotFound, err)
	}

	messages, err := c.store.ListAgentMessages(ctx, sessionID, 50)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}

	claims, err := c.store.ListClaimsByGoal(ctx, sess.GoalID, sess.GoalRevision)
	if err != nil {
		return nil, fmt.Errorf("list claims: %w", err)
	}

	return &TeamSessionOverview{
		Session:      *sess,
		RecentTurns:  messages,
		ActiveClaims: claims,
	}, nil
}
