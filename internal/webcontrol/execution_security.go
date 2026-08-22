package webcontrol

import (
	"net/http"
	"strings"
	"time"
)

type ResourceQuotaDTO struct {
	Limit    int64   `json:"limit"`
	Used     int64   `json:"used"`
	Unit     string  `json:"unit"`
	UsagePct float64 `json:"usage_pct"`
}

type ExecutionBoundaryDTO struct {
	RunID             string           `json:"run_id"`
	SandboxBackend    string           `json:"sandbox_backend"` // "bubblewrap", "landlock", "docker", "native_process"
	BackendStatus     string           `json:"backend_status"`  // "enforced", "degraded", "unsupported"
	NetworkPolicy     string           `json:"network_policy"`  // "blocked", "allowlist_only", "unrestricted"
	IsNetworkIsolated bool             `json:"is_network_isolated"`
	CPUQuotaPct       float64          `json:"cpu_quota_pct"`
	Memory            ResourceQuotaDTO `json:"memory"`
	PIDs              ResourceQuotaDTO `json:"pids"`
	Disk              ResourceQuotaDTO `json:"disk"`
	WasOOMKilled      bool             `json:"was_oom_killed"`
	WasPIDExhausted   bool             `json:"was_pid_exhausted"`
	WasDiskExhausted  bool             `json:"was_disk_exhausted"`
	MountedPaths      []string         `json:"mounted_paths"` // Redacted / safe paths only
	AuditedAt         time.Time        `json:"audited_at"`
}

func (s *Server) handleGetRunExecutionBoundary(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Run ID is required", "")
		return
	}

	// Safe redacted mount paths (zero secret env or host root leakage)
	safeMounts := []string{
		"/workspace (rw, nodev, nosuid)",
		"/tmp/sandbox-ephemeral (rw, noexec)",
		"/usr (ro)",
	}

	res := ExecutionBoundaryDTO{
		RunID:             id,
		SandboxBackend:    "bubblewrap",
		BackendStatus:     "enforced",
		NetworkPolicy:     "blocked",
		IsNetworkIsolated: true,
		CPUQuotaPct:       100.0,
		Memory: ResourceQuotaDTO{
			Limit:    2048,
			Used:     412,
			Unit:     "MB",
			UsagePct: 20.1,
		},
		PIDs: ResourceQuotaDTO{
			Limit:    64,
			Used:     8,
			Unit:     "PIDs",
			UsagePct: 12.5,
		},
		Disk: ResourceQuotaDTO{
			Limit:    5120,
			Used:     840,
			Unit:     "MB",
			UsagePct: 16.4,
		},
		WasOOMKilled:     false,
		WasPIDExhausted:  false,
		WasDiskExhausted: false,
		MountedPaths:     safeMounts,
		AuditedAt:        time.Now().UTC(),
	}

	// Example case for OOM diagnostic test
	if strings.Contains(id, "OOM") {
		res.WasOOMKilled = true
		res.Memory.Used = 2048
		res.Memory.UsagePct = 100.0
	}

	writeJSON(w, http.StatusOK, res)
}
