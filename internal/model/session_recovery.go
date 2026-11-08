package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// SessionRecoveryBinding identifies the reviewed environment, not a provider
// conversation or execution continuation. Digests must be recomputed from live
// canonical observations before adoption. "none" is the explicit no-policy
// digest; an empty digest is missing evidence and is rejected.
type SessionRecoveryBinding struct {
	ProjectID            string `json:"project_id"`
	ProjectRoot          string `json:"project_root"`
	WorktreeDigest       string `json:"worktree_digest"`
	ConstitutionVersion  string `json:"constitution_version"`
	PolicyID             string `json:"policy_id"`
	PolicyVersion        int64  `json:"policy_version"`
	PolicyDigest         string `json:"policy_digest"`
	GoalID               string `json:"goal_id"`
	GoalRevision         int64  `json:"goal_revision"`
	OriginalIntentDigest string `json:"original_intent_digest"`
	GoalBudgetDigest     string `json:"goal_budget_digest"`
	BudgetEvidenceDigest string `json:"budget_evidence_digest"`
	PlanID               string `json:"plan_id,omitempty"`
	PlanVersion          int64  `json:"plan_version,omitempty"`
	PlanDigest           string `json:"plan_digest,omitempty"`
	RunID                string `json:"run_id,omitempty"`
	RunVersion           int64  `json:"run_version,omitempty"`
	RunDigest            string `json:"run_digest,omitempty"`
}

type SessionRecoveryMemoryRef struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}

// SessionRecovery persists a closed, bounded set of references only. It has no
// transcript, composer draft, arbitrary state map, approval, confirmation,
// credential, native-provider resume token, worker lease, or ULTRA lease field.
// Original operator intent is obtained from the exact retained Goal revision.
type SessionRecovery struct {
	Version          int                        `json:"version"`
	ID               string                     `json:"id"`
	Generation       int64                      `json:"generation"`
	SourceSessionID  string                     `json:"source_session_id"`
	Binding          SessionRecoveryBinding     `json:"binding"`
	Memory           []SessionRecoveryMemoryRef `json:"memory"`
	State            string                     `json:"state"` // recoverable, closed, adopted
	AdoptedSessionID string                     `json:"adopted_session_id,omitempty"`
	UpdatedAt        time.Time                  `json:"updated_at"`
	Digest           string                     `json:"digest"`
}

func SessionRecoveryDigest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (r SessionRecovery) ContentDigest() string { r.Digest = ""; return SessionRecoveryDigest(r) }

func (r SessionRecovery) Validate() error {
	b := r.Binding
	if r.Version != 1 || r.ID == "" || len(r.ID) > 256 || r.Generation < 1 || r.SourceSessionID == "" || len(r.SourceSessionID) > 256 || r.UpdatedAt.IsZero() || len(r.Memory) > 128 || b.ProjectID == "" || !filepath.IsAbs(b.ProjectRoot) || filepath.Clean(b.ProjectRoot) != b.ProjectRoot || b.WorktreeDigest == "" || b.ConstitutionVersion == "" || b.PolicyDigest == "" || b.GoalID == "" || b.GoalRevision < 1 || b.OriginalIntentDigest == "" || b.GoalBudgetDigest == "" || b.BudgetEvidenceDigest == "" {
		return ErrInvalid
	}
	if (b.PolicyID == "") != (b.PolicyDigest == "none") || (b.PolicyID != "" && b.PolicyVersion < 1) {
		return ErrInvalid
	}
	if (b.PlanID == "" && (b.PlanVersion != 0 || b.PlanDigest != "")) || (b.PlanID != "" && (b.PlanVersion < 1 || b.PlanDigest == "")) || (b.RunID == "" && (b.RunVersion != 0 || b.RunDigest != "")) || (b.RunID != "" && (b.RunVersion < 1 || b.RunDigest == "" || b.PlanID == "")) {
		return ErrInvalid
	}
	if r.State != "recoverable" && r.State != "closed" && r.State != "adopted" {
		return ErrInvalid
	}
	if (r.State == "adopted") != (r.AdoptedSessionID != "") {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, m := range r.Memory {
		if m.ID == "" || len(m.ID) > 256 || m.Revision < 0 || m.Digest == "" || seen[m.ID] {
			return ErrInvalid
		}
		seen[m.ID] = true
	}
	bts, _ := json.Marshal(r)
	if len(bts) > 65536 || strings.ContainsAny(r.ID+r.SourceSessionID+r.AdoptedSessionID, "\r\n\x1b") {
		return ErrInvalid
	}
	if r.Digest == "" || r.Digest != r.ContentDigest() {
		return ErrInvalid
	}
	return nil
}
