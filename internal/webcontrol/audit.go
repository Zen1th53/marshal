package webcontrol

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type AuditEventsResponseDTO struct {
	Events     []AuditEventDTO `json:"events"`
	TotalCount int             `json:"total_count"`
	Limit      int             `json:"limit"`
	Offset     int             `json:"offset"`
}

var mockAuditLog = []AuditEventDTO{
	{
		ID: "AUD-001",
		Actor: AuditActorDTO{
			PrincipalID: "operator-zen1th53",
			Role:        "admin",
		},
		Action:        "task.merge",
		ResourceType:  "task",
		ResourceID:    "TASK-003-SECURITY-AUDIT",
		Outcome:       "success",
		CorrelationID: "req-merge-TASK-003",
		Timestamp:     time.Now().UTC().Add(-10 * time.Minute),
		Details: map[string]any{
			"merge_commit": "mrg-9999-7d17fb8",
			"strategy":     "squash",
			"quorum":       "2/2",
		},
	},
	{
		ID: "AUD-002",
		Actor: AuditActorDTO{
			PrincipalID: "auditor-claude",
			Role:        "qa_lead",
		},
		Action:        "quorum.attest",
		ResourceType:  "task",
		ResourceID:    "TASK-002-CONTROL-PLANE",
		Outcome:       "success",
		CorrelationID: "req-attest-002",
		Timestamp:     time.Now().UTC().Add(-30 * time.Minute),
		Details: map[string]any{
			"decision": "approved",
			"provider": "anthropic",
		},
	},
	{
		ID: "AUD-003",
		Actor: AuditActorDTO{
			PrincipalID: "unauthorized-guest",
			Role:        "anonymous",
		},
		Action:        "task.delete",
		ResourceType:  "task",
		ResourceID:    "TASK-001-CORE-MEMORY",
		Outcome:       "denied",
		CorrelationID: "req-denied-003",
		Timestamp:     time.Now().UTC().Add(-1 * time.Hour),
		Details: map[string]any{
			"denial_reason": "Missing required authority cap:task:delete",
		},
	},
}

func (s *Server) handleListAuditEvents(w http.ResponseWriter, r *http.Request) {
	outcomeFilter := strings.TrimSpace(r.URL.Query().Get("outcome"))
	actionFilter := strings.TrimSpace(r.URL.Query().Get("action"))
	actorFilter := strings.TrimSpace(r.URL.Query().Get("actor"))
	corrFilter := strings.TrimSpace(r.URL.Query().Get("correlation_id"))

	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	offset := 0
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
			offset = o
		}
	}

	var filtered []AuditEventDTO
	for _, ev := range mockAuditLog {
		if outcomeFilter != "" && outcomeFilter != "all" && ev.Outcome != outcomeFilter {
			continue
		}
		if actionFilter != "" && actionFilter != "all" && ev.Action != actionFilter {
			continue
		}
		if actorFilter != "" && ev.Actor.PrincipalID != actorFilter {
			continue
		}
		if corrFilter != "" && ev.CorrelationID != corrFilter {
			continue
		}
		filtered = append(filtered, ev)
	}

	total := len(filtered)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	paged := filtered[start:end]
	writeJSON(w, http.StatusOK, AuditEventsResponseDTO{
		Events:     paged,
		TotalCount: total,
		Limit:      limit,
		Offset:     offset,
	})
}

func (s *Server) handleExportAuditEvents(w http.ResponseWriter, r *http.Request) {
	user := s.getAuthenticatedUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="marshal_audit_export.json"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(map[string]any{
		"exported_at": time.Now().UTC(),
		"exported_by": user.PrincipalID,
		"events":      mockAuditLog,
	})
}
