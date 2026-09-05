package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrCollaborationInvalid = errors.New("invalid collaboration entity")
)

// Participant describes an agent participating in a multi-agent team session.
// Invariant: Role is fixed; Harness and Model identity are separated from Role.
type Participant struct {
	AgentID  string `json:"agent_id"`
	Role     Role   `json:"role"`
	Harness  string `json:"harness"`
	Model    string `json:"model,omitempty"`
	IsActive bool   `json:"is_active"`
}

func (p Participant) Validate() error {
	if strings.TrimSpace(p.AgentID) == "" {
		return fmt.Errorf("%w: agent ID is required", ErrCollaborationInvalid)
	}
	if strings.TrimSpace(string(p.Role)) == "" {
		return fmt.Errorf("%w: role is required", ErrCollaborationInvalid)
	}
	if strings.TrimSpace(p.Harness) == "" {
		return fmt.Errorf("%w: harness is required", ErrCollaborationInvalid)
	}
	return nil
}

// TeamSession coordinates multi-agent collaboration around a single GoalContract.
type TeamSession struct {
	SessionID    string        `json:"session_id"`
	GoalID       string        `json:"goal_id"`
	GoalRevision int64         `json:"goal_revision"`
	Participants []Participant `json:"participants"`
	ActiveTurn   string        `json:"active_turn"`
	TurnSequence int64         `json:"turn_sequence"`
	Status       string        `json:"status"` // "ACTIVE", "PAUSED", "COMPLETED", "TERMINATED"
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

func (s TeamSession) Validate() error {
	if strings.TrimSpace(s.SessionID) == "" {
		return fmt.Errorf("%w: session ID is required", ErrCollaborationInvalid)
	}
	if strings.TrimSpace(s.GoalID) == "" {
		return fmt.Errorf("%w: goal ID is required", ErrCollaborationInvalid)
	}
	if s.GoalRevision < 0 {
		return fmt.Errorf("%w: goal revision cannot be negative", ErrCollaborationInvalid)
	}
	if len(s.Participants) == 0 {
		return fmt.Errorf("%w: session requires at least one participant", ErrCollaborationInvalid)
	}
	for _, p := range s.Participants {
		if err := p.Validate(); err != nil {
			return err
		}
	}
	if s.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at timestamp required", ErrCollaborationInvalid)
	}
	return nil
}

type MessageKind string

const (
	MessageQuestion            MessageKind = "QUESTION"
	MessageAnswer              MessageKind = "ANSWER"
	MessageClaimChallenge      MessageKind = "CLAIM_CHALLENGE"
	MessageVerificationRequest MessageKind = "VERIFICATION_REQUEST"
	MessageHandoffProposal     MessageKind = "HANDOFF_PROPOSAL"
	MessageFinding             MessageKind = "FINDING"
	MessageFailedApproach      MessageKind = "FAILED_APPROACH"
)

// AgentMessage represents typed communication between collaborative agents.
type AgentMessage struct {
	ID          string           `json:"id"`
	SessionID   string           `json:"session_id"`
	TaskID      string           `json:"task_id,omitempty"`
	From        AuthorProvenance `json:"from"`
	To          string           `json:"to,omitempty"` // empty = broadcast to team
	Kind        MessageKind      `json:"kind"`
	Content     string           `json:"content"`
	ClaimIDs    []string         `json:"claim_ids,omitempty"`
	EvidenceIDs []string         `json:"evidence_ids,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
}

func (m AgentMessage) Validate() error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("%w: message ID is required", ErrCollaborationInvalid)
	}
	if strings.TrimSpace(m.SessionID) == "" {
		return fmt.Errorf("%w: session ID is required", ErrCollaborationInvalid)
	}
	if strings.TrimSpace(m.From.AgentID) == "" {
		return fmt.Errorf("%w: from agent ID is required", ErrCollaborationInvalid)
	}
	if strings.TrimSpace(string(m.Kind)) == "" {
		return fmt.Errorf("%w: message kind is required", ErrCollaborationInvalid)
	}
	if strings.TrimSpace(m.Content) == "" {
		return fmt.Errorf("%w: message content is required", ErrCollaborationInvalid)
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at timestamp required", ErrCollaborationInvalid)
	}
	return nil
}

type LoopKind string

const (
	LoopRepeatedClaim  LoopKind = "REPEATED_CLAIM"
	LoopPingPong       LoopKind = "PING_PONG"
	LoopNoProgress     LoopKind = "NO_PROGRESS"
	LoopBouncedHandoff LoopKind = "BOUNCED_HANDOFF"
)

// LoopDetectionResult records loop protection evaluation across collaborative turns.
type LoopDetectionResult struct {
	LoopDetected    bool     `json:"loop_detected"`
	LoopKind        LoopKind `json:"loop_kind,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	OffendingAgents []string `json:"offending_agents,omitempty"`
}
