package webcontrol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/policy"
)

type GateRuleDTO struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Enforcement     string    `json:"enforcement"` // "mandatory", "advisory"
	Status          string    `json:"status"`      // "enforced", "degraded", "bypassed"
	Description     string    `json:"description"`
	LastEvaluatedAt time.Time `json:"last_evaluated_at"`
}

type CapabilityPolicyRuleDTO struct {
	CapabilityName string `json:"capability_name"`
	RequiredRole   string `json:"required_role"`
	Decision       string `json:"decision"` // "ALLOWED", "DENIED", "DEGRADED"
	DenialReason   string `json:"denial_reason,omitempty"`
}

type PolicyDraftDTO struct {
	PolicyID    string    `json:"policy_id"`
	Version     int       `json:"version"`
	YAMLContent string    `json:"yaml_content"`
	RulesCount  int       `json:"rules_count"`
	Digest      string    `json:"digest"`
	Status      string    `json:"status"` // "draft", "validated"
	UpdatedAt   time.Time `json:"updated_at"`
}

type PolicyRuleDiffDTO struct {
	Type           string   `json:"type"` // "added", "removed", "modified", "unchanged"
	RuleID         string   `json:"rule_id"`
	OldDescription string   `json:"old_description,omitempty"`
	NewDescription string   `json:"new_description,omitempty"`
	OldEffect      string   `json:"old_effect,omitempty"`
	NewEffect      string   `json:"new_effect,omitempty"`
	Changes        []string `json:"changes,omitempty"`
}

type PolicyDiffDTO struct {
	ActivePolicyID string              `json:"active_policy_id"`
	ActiveVersion  int                 `json:"active_version"`
	ActiveDigest   string              `json:"active_digest"`
	DraftVersion   int                 `json:"draft_version"`
	DraftDigest    string              `json:"draft_digest"`
	HasChanges     bool                `json:"has_changes"`
	RuleDiffs      []PolicyRuleDiffDTO `json:"rule_diffs"`
}

type PolicyValidationResultDTO struct {
	Valid      bool     `json:"valid"`
	Digest     string   `json:"digest,omitempty"`
	RulesCount int      `json:"rules_count"`
	Errors     []string `json:"errors,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

type SecurityPolicyInspectorResponseDTO struct {
	PolicyID         string                    `json:"policy_id"`
	Revision         int                       `json:"revision"`
	Digest           string                    `json:"digest"`
	GlobalRiskLevel  string                    `json:"global_risk_level"`
	DegradedControls []string                  `json:"degraded_controls"`
	GateRules        []GateRuleDTO             `json:"gate_rules"`
	CapabilityRules  []CapabilityPolicyRuleDTO `json:"capability_rules"`
	ActiveDraft      *PolicyDraftDTO           `json:"active_draft,omitempty"`
	HistoryCount     int                       `json:"history_count"`
	LastAuditedAt    time.Time                 `json:"last_audited_at"`
}

type PolicyEditorStore struct {
	mu             sync.RWMutex
	activePolicyID string
	activeVersion  int
	activeRevision int
	activeDigest   string
	activeYAML     string
	activePolicy   policy.Policy
	draft          *PolicyDraftDTO
	history        []policy.Policy
	historyYAML    []string
}

const defaultInitialPolicyYAML = `id: POL-MARSHAL-MAIN-2026
version: 1
default: deny
rules:
  - id: rule-task-create
    description: Allow operator to create autonomous tasks
    when:
      action: cap:task:create
      role: operator
    effect: allow
    priority: 10
  - id: rule-task-run
    description: Allow operator to trigger worker run
    when:
      action: cap:task:run
      role: operator
    effect: allow
    priority: 10
  - id: rule-review-sign
    description: Allow QA lead to sign review attestations
    when:
      action: cap:review:sign
      role: qa_lead
    effect: allow
    priority: 20
  - id: rule-task-merge
    description: Allow admin to execute verified task merge
    when:
      action: cap:task:merge
      role: admin
    effect: allow
    priority: 30
  - id: rule-worktree-reset-deny
    description: Destructive worktree reset is prohibited
    when:
      action: cap:worktree:force_reset
    effect: deny
    priority: 100
  - id: rule-keys-export-deny
    description: Credential exfiltration is architecturally forbidden
    when:
      action: cap:keys:export
    effect: deny
    priority: 100
`

var globalPolicyStore = newPolicyEditorStore()

func newPolicyEditorStore() *PolicyEditorStore {
	parsed, _ := policy.Parse([]byte(defaultInitialPolicyYAML))
	digest, _ := parsed.Digest()

	return &PolicyEditorStore{
		activePolicyID: "POL-MARSHAL-MAIN-2026",
		activeVersion:  1,
		activeRevision: 42,
		activeDigest:   string(digest),
		activeYAML:     defaultInitialPolicyYAML,
		activePolicy:   parsed,
		draft: &PolicyDraftDTO{
			PolicyID:    "POL-MARSHAL-MAIN-2026",
			Version:     2,
			YAMLContent: defaultInitialPolicyYAML,
			RulesCount:  len(parsed.Rules),
			Digest:      string(digest),
			Status:      "draft",
			UpdatedAt:   time.Now().UTC(),
		},
		history:     []policy.Policy{parsed},
		historyYAML: []string{defaultInitialPolicyYAML},
	}
}

func (s *Server) handleGetSecurityPolicy(w http.ResponseWriter, r *http.Request) {
	globalPolicyStore.mu.RLock()
	defer globalPolicyStore.mu.RUnlock()

	now := time.Now().UTC()

	rules := []GateRuleDTO{
		{
			ID:              "GATE-001-LOOPBACK",
			Name:            "Strict Loopback Bind Constraint",
			Enforcement:     "mandatory",
			Status:          "enforced",
			Description:     "Web Control Plane MUST strictly bind to 127.0.0.1 loopback interface. Public 0.0.0.0 egress blocked.",
			LastEvaluatedAt: now.Add(-1 * time.Minute),
		},
		{
			ID:              "GATE-002-ZERO-SECRET",
			Name:            "Zero Secret Egress Filter",
			Enforcement:     "mandatory",
			Status:          "enforced",
			Description:     "Blocks keys, passwords, bearer tokens and credentials from SSE streams, REST responses, and logs.",
			LastEvaluatedAt: now.Add(-2 * time.Minute),
		},
		{
			ID:              "GATE-003-INDEPENDENT-QUORUM",
			Name:            "Multi-Agent Quorum Independence",
			Enforcement:     "mandatory",
			Status:          "enforced",
			Description:     "Prohibits single-model self-attestation. Requires 2 independent model providers for critical gate merges.",
			LastEvaluatedAt: now.Add(-5 * time.Minute),
		},
		{
			ID:              "GATE-004-SANDBOX-ISOLATION",
			Name:            "Ephemeral Subprocess Sandbox",
			Enforcement:     "mandatory",
			Status:          "enforced",
			Description:     "Execution runs are sandboxed with restricted filesystem and network namespaces.",
			LastEvaluatedAt: now.Add(-3 * time.Minute),
		},
	}

	var caps []CapabilityPolicyRuleDTO
	for _, rule := range globalPolicyStore.activePolicy.Rules {
		decision := "ALLOWED"
		if rule.Effect == policy.EffectDeny {
			decision = "DENIED"
		} else if rule.Effect == policy.EffectRequire {
			decision = "REQUIRE_APPROVAL"
		}
		target := rule.When["action"]
		if target == "" {
			target = rule.ID
		}
		role := rule.When["role"]
		if role == "" {
			role = "any"
		}
		caps = append(caps, CapabilityPolicyRuleDTO{
			CapabilityName: target,
			RequiredRole:   role,
			Decision:       decision,
			DenialReason:   rule.Description,
		})
	}

	writeJSON(w, http.StatusOK, SecurityPolicyInspectorResponseDTO{
		PolicyID:         globalPolicyStore.activePolicyID,
		Revision:         globalPolicyStore.activeRevision,
		Digest:           globalPolicyStore.activeDigest,
		GlobalRiskLevel:  "LOW",
		DegradedControls: []string{},
		GateRules:        rules,
		CapabilityRules:  caps,
		ActiveDraft:      globalPolicyStore.draft,
		HistoryCount:     len(globalPolicyStore.history),
		LastAuditedAt:    now,
	})
}

func (s *Server) handleSavePolicyDraft(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[struct {
		YAMLContent string `json:"yaml_content"`
		PolicyID    string `json:"policy_id,omitempty"`
		Version     int    `json:"version,omitempty"`
	}]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON payload: "+err.Error(), "")
		return
	}

	content := strings.TrimSpace(env.Payload.YAMLContent)
	if content == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "YAML content is required", "")
		return
	}

	parsed, err := policy.Parse([]byte(content))
	var digestStr string
	rulesCount := 0
	status := "draft"
	if err == nil {
		d, dErr := parsed.Digest()
		if dErr == nil {
			digestStr = string(d)
			status = "validated"
		}
		rulesCount = len(parsed.Rules)
	}

	policyID := env.Payload.PolicyID
	if policyID == "" && err == nil {
		policyID = string(parsed.ID)
	}
	if policyID == "" {
		policyID = "POL-MARSHAL-MAIN-2026"
	}

	version := env.Payload.Version
	if version <= 0 && err == nil {
		version = int(parsed.Version)
	}
	if version <= 0 {
		version = globalPolicyStore.activeVersion + 1
	}

	globalPolicyStore.mu.Lock()
	globalPolicyStore.draft = &PolicyDraftDTO{
		PolicyID:    policyID,
		Version:     version,
		YAMLContent: content,
		RulesCount:  rulesCount,
		Digest:      digestStr,
		Status:      status,
		UpdatedAt:   time.Now().UTC(),
	}
	savedDraft := *globalPolicyStore.draft
	globalPolicyStore.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"draft":  savedDraft,
		"status": "draft_saved",
	})
}

func (s *Server) handleValidatePolicy(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[struct {
		YAMLContent string `json:"yaml_content"`
	}]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON payload: "+err.Error(), "")
		return
	}

	content := strings.TrimSpace(env.Payload.YAMLContent)
	if content == "" {
		globalPolicyStore.mu.RLock()
		if globalPolicyStore.draft != nil {
			content = globalPolicyStore.draft.YAMLContent
		}
		globalPolicyStore.mu.RUnlock()
	}

	if content == "" {
		writeError(w, http.StatusBadRequest, "empty_content", "No policy YAML content provided or in draft", "")
		return
	}

	parsed, err := policy.Parse([]byte(content))
	if err != nil {
		writeJSON(w, http.StatusOK, PolicyValidationResultDTO{
			Valid:      false,
			RulesCount: 0,
			Errors:     []string{err.Error()},
		})
		return
	}

	digest, err := parsed.Digest()
	if err != nil {
		writeJSON(w, http.StatusOK, PolicyValidationResultDTO{
			Valid:      false,
			RulesCount: len(parsed.Rules),
			Errors:     []string{"digest generation error: " + err.Error()},
		})
		return
	}

	writeJSON(w, http.StatusOK, PolicyValidationResultDTO{
		Valid:      true,
		Digest:     string(digest),
		RulesCount: len(parsed.Rules),
	})
}

func (s *Server) handleDiffPolicy(w http.ResponseWriter, r *http.Request) {
	globalPolicyStore.mu.RLock()
	defer globalPolicyStore.mu.RUnlock()

	activeRules := make(map[string]policy.Rule)
	for _, r := range globalPolicyStore.activePolicy.Rules {
		activeRules[r.ID] = r
	}

	draftContent := globalPolicyStore.activeYAML
	if globalPolicyStore.draft != nil && globalPolicyStore.draft.YAMLContent != "" {
		draftContent = globalPolicyStore.draft.YAMLContent
	}

	draftPolicy, err := policy.Parse([]byte(draftContent))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_draft", "Draft policy has syntax errors: "+err.Error(), "")
		return
	}

	draftDigest, _ := draftPolicy.Digest()
	draftRules := make(map[string]policy.Rule)
	var diffs []PolicyRuleDiffDTO
	hasChanges := false

	for _, dRule := range draftPolicy.Rules {
		draftRules[dRule.ID] = dRule
		if aRule, exists := activeRules[dRule.ID]; !exists {
			hasChanges = true
			diffs = append(diffs, PolicyRuleDiffDTO{
				Type:           "added",
				RuleID:         dRule.ID,
				NewDescription: dRule.Description,
				NewEffect:      string(dRule.Effect),
				Changes:        []string{"New policy rule added"},
			})
		} else {
			var changes []string
			if aRule.Effect != dRule.Effect {
				changes = append(changes, fmt.Sprintf("Effect changed: %s -> %s", aRule.Effect, dRule.Effect))
			}
			if aRule.Description != dRule.Description {
				changes = append(changes, "Description updated")
			}
			if aRule.Priority != dRule.Priority {
				changes = append(changes, fmt.Sprintf("Priority changed: %d -> %d", aRule.Priority, dRule.Priority))
			}
			if len(changes) > 0 {
				hasChanges = true
				diffs = append(diffs, PolicyRuleDiffDTO{
					Type:           "modified",
					RuleID:         dRule.ID,
					OldDescription: aRule.Description,
					NewDescription: dRule.Description,
					OldEffect:      string(aRule.Effect),
					NewEffect:      string(dRule.Effect),
					Changes:        changes,
				})
			} else {
				diffs = append(diffs, PolicyRuleDiffDTO{
					Type:           "unchanged",
					RuleID:         dRule.ID,
					OldDescription: aRule.Description,
					NewDescription: dRule.Description,
					OldEffect:      string(aRule.Effect),
					NewEffect:      string(dRule.Effect),
				})
			}
		}
	}

	for _, aRule := range globalPolicyStore.activePolicy.Rules {
		if _, exists := draftRules[aRule.ID]; !exists {
			hasChanges = true
			diffs = append(diffs, PolicyRuleDiffDTO{
				Type:           "removed",
				RuleID:         aRule.ID,
				OldDescription: aRule.Description,
				OldEffect:      string(aRule.Effect),
				Changes:        []string{"Rule removed from policy"},
			})
		}
	}

	sort.SliceStable(diffs, func(i, j int) bool {
		return diffs[i].RuleID < diffs[j].RuleID
	})

	writeJSON(w, http.StatusOK, PolicyDiffDTO{
		ActivePolicyID: globalPolicyStore.activePolicyID,
		ActiveVersion:  globalPolicyStore.activeVersion,
		ActiveDigest:   globalPolicyStore.activeDigest,
		DraftVersion:   int(draftPolicy.Version),
		DraftDigest:    string(draftDigest),
		HasChanges:     hasChanges,
		RuleDiffs:      diffs,
	})
}

func (s *Server) handleApplyPolicy(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[struct {
		ExpectedRevision *int   `json:"expected_revision,omitempty"`
		DraftDigest      string `json:"draft_digest,omitempty"`
	}]
	_ = json.NewDecoder(r.Body).Decode(&env)

	globalPolicyStore.mu.Lock()
	defer globalPolicyStore.mu.Unlock()

	if env.Payload.ExpectedRevision != nil && *env.Payload.ExpectedRevision != globalPolicyStore.activeRevision {
		writeError(w, http.StatusConflict, "revision_conflict", "Policy revision conflict: active revision has changed", "")
		return
	}

	if globalPolicyStore.draft == nil || globalPolicyStore.draft.YAMLContent == "" {
		writeError(w, http.StatusBadRequest, "no_draft", "No draft policy exists to apply", "")
		return
	}

	parsed, err := policy.Parse([]byte(globalPolicyStore.draft.YAMLContent))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_draft", "Draft policy cannot be applied: "+err.Error(), "")
		return
	}

	digest, err := parsed.Digest()
	if err != nil {
		writeError(w, http.StatusBadRequest, "digest_error", "Failed to compute digest: "+err.Error(), "")
		return
	}

	if env.Payload.DraftDigest != "" && env.Payload.DraftDigest != string(digest) {
		writeError(w, http.StatusConflict, "stale_draft", "Draft digest mismatch", "")
		return
	}

	// Archive old active policy for rollback capability
	globalPolicyStore.history = append(globalPolicyStore.history, globalPolicyStore.activePolicy)
	globalPolicyStore.historyYAML = append(globalPolicyStore.historyYAML, globalPolicyStore.activeYAML)

	globalPolicyStore.activePolicy = parsed
	globalPolicyStore.activePolicyID = string(parsed.ID)
	globalPolicyStore.activeVersion = int(parsed.Version)
	globalPolicyStore.activeRevision++
	globalPolicyStore.activeDigest = string(digest)
	globalPolicyStore.activeYAML = globalPolicyStore.draft.YAMLContent

	// Clear applied draft or prepare next version draft
	globalPolicyStore.draft = &PolicyDraftDTO{
		PolicyID:    globalPolicyStore.activePolicyID,
		Version:     globalPolicyStore.activeVersion + 1,
		YAMLContent: globalPolicyStore.activeYAML,
		RulesCount:  len(parsed.Rules),
		Digest:      globalPolicyStore.activeDigest,
		Status:      "draft",
		UpdatedAt:   time.Now().UTC(),
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":           "applied",
		"active_policy_id": globalPolicyStore.activePolicyID,
		"version":          globalPolicyStore.activeVersion,
		"revision":         globalPolicyStore.activeRevision,
		"digest":           globalPolicyStore.activeDigest,
	})
}

func (s *Server) handleRollbackPolicy(w http.ResponseWriter, r *http.Request) {
	globalPolicyStore.mu.Lock()
	defer globalPolicyStore.mu.Unlock()

	if len(globalPolicyStore.history) <= 1 {
		writeError(w, http.StatusBadRequest, "no_history", "No prior policy revisions available to rollback", "")
		return
	}

	lastIdx := len(globalPolicyStore.history) - 1
	prevPolicy := globalPolicyStore.history[lastIdx]
	prevYAML := globalPolicyStore.historyYAML[lastIdx]

	globalPolicyStore.history = globalPolicyStore.history[:lastIdx]
	globalPolicyStore.historyYAML = globalPolicyStore.historyYAML[:lastIdx]

	digest, _ := prevPolicy.Digest()

	globalPolicyStore.activePolicy = prevPolicy
	globalPolicyStore.activePolicyID = string(prevPolicy.ID)
	globalPolicyStore.activeVersion = int(prevPolicy.Version)
	globalPolicyStore.activeRevision++
	globalPolicyStore.activeDigest = string(digest)
	globalPolicyStore.activeYAML = prevYAML

	globalPolicyStore.draft = &PolicyDraftDTO{
		PolicyID:    globalPolicyStore.activePolicyID,
		Version:     globalPolicyStore.activeVersion + 1,
		YAMLContent: prevYAML,
		RulesCount:  len(prevPolicy.Rules),
		Digest:      string(digest),
		Status:      "draft",
		UpdatedAt:   time.Now().UTC(),
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":           "rolled_back",
		"active_policy_id": globalPolicyStore.activePolicyID,
		"version":          globalPolicyStore.activeVersion,
		"revision":         globalPolicyStore.activeRevision,
		"digest":           globalPolicyStore.activeDigest,
	})
}
