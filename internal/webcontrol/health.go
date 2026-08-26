package webcontrol

import (
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
)

type DiagnosticCheckDTO struct {
	Component string `json:"component"`
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Message   string `json:"message"`
}

type DoctorReportDTO struct {
	OverallStatus string               `json:"overall_status"`
	Checks        []DiagnosticCheckDTO `json:"checks"`
	EvaluatedAt   time.Time            `json:"evaluated_at"`
}

func timedDiagnostic(component string, probe func() (string, error)) DiagnosticCheckDTO {
	started := time.Now()
	message, err := probe()
	check := DiagnosticCheckDTO{Component: component, Status: "READY", LatencyMs: time.Since(started).Milliseconds(), Message: message}
	if err != nil {
		check.Status = "FAILED"
		check.Message = err.Error()
	}
	return check
}

func unknownDiagnostic(component, message string) DiagnosticCheckDTO {
	return DiagnosticCheckDTO{Component: component, Status: "UNKNOWN", Message: message}
}

func (s *Server) handleGetDoctorReport(w http.ResponseWriter, r *http.Request) {
	checks := make([]DiagnosticCheckDTO, 0, 7)
	if s.store == nil {
		checks = append(checks, unknownDiagnostic("database_sqlite", "no live runtime store is attached"))
	} else {
		checks = append(checks, timedDiagnostic("database_sqlite", func() (string, error) {
			version, err := s.store.SchemaVersion(r.Context())
			if err != nil {
				return "", err
			}
			if version != store.LatestSchemaVersion {
				return "", fmt.Errorf("schema version %d; expected %d", version, store.LatestSchemaVersion)
			}
			if err := s.store.Integrity(r.Context()); err != nil {
				return "", err
			}
			return fmt.Sprintf("SQLite integrity ok; schema version %d", version), nil
		}))
	}

	hub := s.sseHub.Status()
	checks = append(checks, DiagnosticCheckDTO{Component: "event_bus", Status: "READY", Message: fmt.Sprintf("bounded SSE replay %d/%d; clients=%d; sequence=%d", hub.Buffered, hub.BufferLimit, hub.Clients, hub.CurrentSeq)})
	checks = append(checks, unknownDiagnostic("worker_fleet", "runtime exposes no truthful worker-liveness probe"))
	checks = append(checks, unknownDiagnostic("providers", "provider reachability is not probed by this read-only endpoint"))
	if s.memory == nil || s.store == nil {
		checks = append(checks, unknownDiagnostic("memory_indexes", "no canonical memory service is attached"))
	} else {
		checks = append(checks, DiagnosticCheckDTO{Component: "memory_indexes", Status: "DEGRADED", Message: "canonical memory service attached; projection parity requires explicit rebuild verification"})
	}
	checks = append(checks, timedDiagnostic("sandbox_isolation", func() (string, error) {
		path, err := exec.LookPath("bwrap")
		if err != nil {
			return "", fmt.Errorf("bubblewrap unavailable: %w", err)
		}
		return "bubblewrap available at " + path, nil
	}))
	checks = append(checks, unknownDiagnostic("version_integrity", "release manifest verification was not executed by this endpoint"))

	overall := "READY"
	for _, check := range checks {
		if check.Status == "FAILED" {
			overall = "FAILED"
			break
		}
		if check.Status == "DEGRADED" || check.Status == "UNKNOWN" {
			overall = "DEGRADED"
		}
	}
	writeJSON(w, http.StatusOK, DoctorReportDTO{OverallStatus: overall, Checks: checks, EvaluatedAt: time.Now().UTC()})
}
