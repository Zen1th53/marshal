package webcontrol

import (
	"net/http"
	"time"
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

type SecurityPolicyInspectorResponseDTO struct {
	PolicyID         string                    `json:"policy_id"`
	Revision         int                       `json:"revision"`
	GlobalRiskLevel  string                    `json:"global_risk_level"`
	DegradedControls []string                  `json:"degraded_controls"`
	GateRules        []GateRuleDTO             `json:"gate_rules"`
	CapabilityRules  []CapabilityPolicyRuleDTO `json:"capability_rules"`
	LastAuditedAt    time.Time                 `json:"last_audited_at"`
}

func (s *Server) handleGetSecurityPolicy(w http.ResponseWriter, r *http.Request) {
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

	caps := []CapabilityPolicyRuleDTO{
		{
			CapabilityName: "cap:task:create",
			RequiredRole:   "operator",
			Decision:       "ALLOWED",
		},
		{
			CapabilityName: "cap:task:run",
			RequiredRole:   "operator",
			Decision:       "ALLOWED",
		},
		{
			CapabilityName: "cap:review:sign",
			RequiredRole:   "qa_lead",
			Decision:       "ALLOWED",
		},
		{
			CapabilityName: "cap:task:merge",
			RequiredRole:   "admin",
			Decision:       "ALLOWED",
		},
		{
			CapabilityName: "cap:worktree:force_reset",
			RequiredRole:   "system",
			Decision:       "DENIED",
			DenialReason:   "Direct destructive worktree mutations are prohibited. Use checkpoint recovery API.",
		},
		{
			CapabilityName: "cap:keys:export",
			RequiredRole:   "none",
			Decision:       "DENIED",
			DenialReason:   "Credential exfiltration is architecturally forbidden.",
		},
	}

	writeJSON(w, http.StatusOK, SecurityPolicyInspectorResponseDTO{
		PolicyID:         "POL-MARSHAL-MAIN-2026",
		Revision:         42,
		GlobalRiskLevel:  "LOW",
		DegradedControls: []string{},
		GateRules:        rules,
		CapabilityRules:  caps,
		LastAuditedAt:    now,
	})
}
