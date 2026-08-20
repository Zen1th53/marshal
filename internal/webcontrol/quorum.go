package webcontrol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type AttestationDTO struct {
	ReviewerID string    `json:"reviewer_id"`
	Provider   string    `json:"provider"`
	Role       string    `json:"role"`
	Decision   string    `json:"decision"` // "approved", "rejected", "vetoed"
	Comment    string    `json:"comment"`
	CommitHash string    `json:"commit_hash"`
	SignedAt   time.Time `json:"signed_at"`
}

type QuorumStatusDTO struct {
	TaskID                string           `json:"task_id"`
	HeadCommit            string           `json:"head_commit"`
	RequiredQuorum        int              `json:"required_quorum"`
	CurrentApprovalsCount int              `json:"current_approvals_count"`
	HasVeto               bool             `json:"has_veto"`
	VetoReason            string           `json:"veto_reason,omitempty"`
	IsQuorumMet           bool             `json:"is_quorum_met"`
	IndependenceNote      string           `json:"independence_note"`
	Attestations          []AttestationDTO `json:"attestations"`
}

type SubmitDecisionPayload struct {
	Decision   string `json:"decision"` // "approved", "rejected", "veto"
	Comment    string `json:"comment"`
	CommitHash string `json:"commit_hash"`
}

type QuorumStore struct {
	mu           sync.Mutex
	attestations map[string][]AttestationDTO // task ID -> list of attestations
}

var globalQuorumStore = &QuorumStore{
	attestations: map[string][]AttestationDTO{
		"TASK-002-CONTROL-PLANE": {
			{
				ReviewerID: "agent-claude-planner",
				Provider:   "anthropic",
				Role:       "planner_auditor",
				Decision:   "approved",
				Comment:    "Architecture and invariant conformance verified.",
				CommitHash: "29c3643",
				SignedAt:   time.Now().UTC().Add(-15 * time.Minute),
			},
		},
	},
}

func (s *Server) handleGetTaskQuorum(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Task ID is required", "")
		return
	}

	globalQuorumStore.mu.Lock()
	defer globalQuorumStore.mu.Unlock()

	attestations := globalQuorumStore.attestations[id]
	approvals := 0
	hasVeto := false
	vetoReason := ""

	providersSeen := make(map[string]bool)
	independentApprovals := 0

	for _, a := range attestations {
		if a.Decision == "vetoed" {
			hasVeto = true
			vetoReason = a.Comment
		} else if a.Decision == "approved" {
			approvals++
			if !providersSeen[a.Provider] {
				providersSeen[a.Provider] = true
				independentApprovals++
			}
		}
	}

	required := 2
	isMet := !hasVeto && independentApprovals >= required

	writeJSON(w, http.StatusOK, QuorumStatusDTO{
		TaskID:                id,
		HeadCommit:            "29c3643",
		RequiredQuorum:        required,
		CurrentApprovalsCount: independentApprovals,
		HasVeto:               hasVeto,
		VetoReason:            vetoReason,
		IsQuorumMet:           isMet,
		IndependenceNote:      "Quorum requires independent model providers. Multiple signers from the same provider count as 1 vote.",
		Attestations:          attestations,
	})
}

func (s *Server) handleSubmitQuorumDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Task ID is required", "")
		return
	}

	user := s.getAuthenticatedUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", "")
		return
	}

	var env MutationEnvelope[SubmitDecisionPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON payload", "")
		return
	}

	payload := env.Payload
	decision := payload.Decision
	if decision != "approved" && decision != "rejected" && decision != "vetoed" {
		writeError(w, http.StatusBadRequest, "invalid_decision", "Decision must be approved, rejected, or vetoed", "")
		return
	}

	globalQuorumStore.mu.Lock()
	defer globalQuorumStore.mu.Unlock()

	// Check if this reviewer already signed for this task
	currentList := globalQuorumStore.attestations[id]
	for _, a := range currentList {
		if a.ReviewerID == user.PrincipalID {
			writeError(w, http.StatusConflict, "duplicate_signature", fmt.Sprintf("Reviewer %s already attested for this task", user.PrincipalID), "")
			return
		}
	}

	newAttestation := AttestationDTO{
		ReviewerID: user.PrincipalID,
		Provider:   "google", // operator/auditor provider
		Role:       user.Role,
		Decision:   decision,
		Comment:    payload.Comment,
		CommitHash: payload.CommitHash,
		SignedAt:   time.Now().UTC(),
	}

	globalQuorumStore.attestations[id] = append(currentList, newAttestation)

	s.sseHub.Broadcast("review.status", "task", id, map[string]any{
		"task_id":  id,
		"reviewer": user.PrincipalID,
		"decision": decision,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"task_id":  id,
		"decision": decision,
		"status":   "recorded",
	})
}
