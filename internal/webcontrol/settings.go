package webcontrol

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type SafeEnvDiagnosticsDTO struct {
	OSArch        string `json:"os_arch"`
	GoVersion     string `json:"go_version"`
	SandboxKind   string `json:"sandbox_kind"`
	StorageEngine string `json:"storage_engine"`
}

type SystemSettingsDTO struct {
	Revision                 int                   `json:"revision"`
	SystemMode               string                `json:"system_mode"` // "standard", "strict", "airgap"
	MaxConcurrentWorkers     int                   `json:"max_concurrent_workers"`
	TelemetryLevel           string                `json:"telemetry_level"` // "minimal", "standard", "verbose"
	AutoConsolidationEnabled bool                  `json:"auto_consolidation_enabled"`
	MemoryRetentionDays      int                   `json:"memory_retention_days"`
	RequiresRestart          bool                  `json:"requires_restart"`
	EnvDiagnostics           SafeEnvDiagnosticsDTO `json:"env_diagnostics"`
	UpdatedAt                time.Time             `json:"updated_at"`
}

type UpdateSettingsPayload struct {
	ExpectedRevision         int    `json:"expected_revision"`
	SystemMode               string `json:"system_mode"`
	MaxConcurrentWorkers     int    `json:"max_concurrent_workers"`
	TelemetryLevel           string `json:"telemetry_level"`
	AutoConsolidationEnabled bool   `json:"auto_consolidation_enabled"`
	MemoryRetentionDays      int    `json:"memory_retention_days"`
}

var (
	settingsMu sync.RWMutex
	appSettings = SystemSettingsDTO{
		Revision:                 1,
		SystemMode:               "strict",
		MaxConcurrentWorkers:     4,
		TelemetryLevel:           "standard",
		AutoConsolidationEnabled: true,
		MemoryRetentionDays:      30,
		RequiresRestart:          false,
		EnvDiagnostics: SafeEnvDiagnosticsDTO{
			OSArch:        "linux/amd64",
			GoVersion:     "go1.24",
			SandboxKind:   "bubblewrap_rootless",
			StorageEngine: "sqlite_wal_canonical",
		},
		UpdatedAt: time.Now().UTC(),
	}
)

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settingsMu.RLock()
	defer settingsMu.RUnlock()

	writeJSON(w, http.StatusOK, appSettings)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[UpdateSettingsPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid settings mutation payload", "")
		return
	}

	payload := env.Payload
	settingsMu.Lock()
	defer settingsMu.Unlock()

	if payload.ExpectedRevision != appSettings.Revision {
		writeError(w, http.StatusConflict, "revision_conflict", "Revision conflict: settings updated concurrently", "")
		return
	}

	// Validate allowed values (mass-assignment and arbitrary config protection)
	if payload.SystemMode != "standard" && payload.SystemMode != "strict" && payload.SystemMode != "airgap" {
		writeError(w, http.StatusBadRequest, "invalid_mode", "SystemMode must be standard, strict, or airgap", "")
		return
	}
	if payload.MaxConcurrentWorkers < 1 || payload.MaxConcurrentWorkers > 16 {
		writeError(w, http.StatusBadRequest, "invalid_workers", "MaxConcurrentWorkers must be between 1 and 16", "")
		return
	}
	if payload.TelemetryLevel != "minimal" && payload.TelemetryLevel != "standard" && payload.TelemetryLevel != "verbose" {
		writeError(w, http.StatusBadRequest, "invalid_telemetry", "TelemetryLevel must be minimal, standard, or verbose", "")
		return
	}
	if payload.MemoryRetentionDays < 1 || payload.MemoryRetentionDays > 365 {
		writeError(w, http.StatusBadRequest, "invalid_retention", "MemoryRetentionDays must be between 1 and 365", "")
		return
	}

	restartRequired := false
	if payload.SystemMode != appSettings.SystemMode || payload.MaxConcurrentWorkers != appSettings.MaxConcurrentWorkers {
		restartRequired = true
	}

	appSettings.Revision++
	appSettings.SystemMode = payload.SystemMode
	appSettings.MaxConcurrentWorkers = payload.MaxConcurrentWorkers
	appSettings.TelemetryLevel = payload.TelemetryLevel
	appSettings.AutoConsolidationEnabled = payload.AutoConsolidationEnabled
	appSettings.MemoryRetentionDays = payload.MemoryRetentionDays
	appSettings.RequiresRestart = restartRequired
	appSettings.UpdatedAt = time.Now().UTC()

	writeJSON(w, http.StatusOK, appSettings)
}
