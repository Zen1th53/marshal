package webcontrol

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type ActiveModelDTO struct {
	ID            string  `json:"id"`
	ContextWindow int     `json:"context_window"`
	LatencyP95Ms  float64 `json:"latency_p95_ms"`
}

type ProviderDTO struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Class        string           `json:"class"` // "cloud", "local"
	ProbeStatus  string           `json:"probe_status"` // "healthy", "degraded", "unavailable", "not_run"
	Capabilities []string         `json:"capabilities"`
	Models       []ActiveModelDTO `json:"models"`
	LastProbedAt time.Time        `json:"last_probed_at"`
}

type RouterDecisionDTO struct {
	Intent        string `json:"intent"` // "planning", "implementation", "security_audit", "offline_fallback"
	SelectedModel string `json:"selected_model"`
	ProviderID    string `json:"provider_id"`
	Rationale     string `json:"rationale"`
	IsPinned      bool   `json:"is_pinned"`
}

type ProviderInventoryResponseDTO struct {
	Providers      []ProviderDTO       `json:"providers"`
	RoutingDecisions []RouterDecisionDTO `json:"routing_decisions"`
	LastEvaluatedAt time.Time           `json:"last_evaluated_at"`
}

type RouterOverridePayload struct {
	Intent   string `json:"intent"`
	ModelID  string `json:"model_id"`
	IsPinned bool   `json:"is_pinned"`
}

type ProviderStore struct {
	mu        sync.Mutex
	overrides map[string]string // intent -> pinned model ID
}

var globalProviderStore = &ProviderStore{
	overrides: make(map[string]string),
}

func (s *Server) handleGetProviders(w http.ResponseWriter, r *http.Request) {
	globalProviderStore.mu.Lock()
	defer globalProviderStore.mu.Unlock()

	now := time.Now().UTC()

	providers := []ProviderDTO{
		{
			ID:          "anthropic",
			Name:        "Anthropic Claude",
			Class:       "cloud",
			ProbeStatus: "healthy",
			Capabilities: []string{"reasoning", "tool_use", "code_generation", "json_mode"},
			Models: []ActiveModelDTO{
				{ID: "claude-3-7-sonnet", ContextWindow: 200000, LatencyP95Ms: 850.0},
				{ID: "claude-3-5-haiku", ContextWindow: 200000, LatencyP95Ms: 280.0},
			},
			LastProbedAt: now.Add(-2 * time.Minute),
		},
		{
			ID:          "google",
			Name:        "Google Gemini",
			Class:       "cloud",
			ProbeStatus: "healthy",
			Capabilities: []string{"multimodal", "1m_context", "code_generation", "tool_use"},
			Models: []ActiveModelDTO{
				{ID: "gemini-2.0-pro-exp", ContextWindow: 1000000, LatencyP95Ms: 620.0},
				{ID: "gemini-2.0-flash", ContextWindow: 1000000, LatencyP95Ms: 210.0},
			},
			LastProbedAt: now.Add(-3 * time.Minute),
		},
		{
			ID:          "openai",
			Name:        "OpenAI Platform",
			Class:       "cloud",
			ProbeStatus: "healthy",
			Capabilities: []string{"code_generation", "structured_outputs", "tool_use"},
			Models: []ActiveModelDTO{
				{ID: "gpt-4o", ContextWindow: 128000, LatencyP95Ms: 540.0},
				{ID: "gpt-4o-mini", ContextWindow: 128000, LatencyP95Ms: 190.0},
			},
			LastProbedAt: now.Add(-1 * time.Minute),
		},
		{
			ID:          "ollama-local",
			Name:        "Local Ollama Engine",
			Class:       "local",
			ProbeStatus: "healthy",
			Capabilities: []string{"offline_airgap", "code_generation"},
			Models: []ActiveModelDTO{
				{ID: "llama-3.3-70b", ContextWindow: 32768, LatencyP95Ms: 1400.0},
				{ID: "qwen2.5-coder-32b", ContextWindow: 32768, LatencyP95Ms: 920.0},
			},
			LastProbedAt: now.Add(-5 * time.Minute),
		},
	}

	routing := []RouterDecisionDTO{
		{
			Intent:        "planning",
			SelectedModel: "claude-3-7-sonnet",
			ProviderID:    "anthropic",
			Rationale:     "Evaluated as optimal for architectural decomposition and formal invariant checks.",
			IsPinned:      globalProviderStore.overrides["planning"] != "",
		},
		{
			Intent:        "implementation",
			SelectedModel: "gpt-4o",
			ProviderID:    "openai",
			Rationale:     "High throughput and fast AST-aligned code generation with low edit collision rate.",
			IsPinned:      globalProviderStore.overrides["implementation"] != "",
		},
		{
			Intent:        "security_audit",
			SelectedModel: "gemini-2.0-pro-exp",
			ProviderID:    "google",
			Rationale:     "Massive 1M token context capacity allows simultaneous whole-codebase AST and taint auditing.",
			IsPinned:      globalProviderStore.overrides["security_audit"] != "",
		},
		{
			Intent:        "offline_fallback",
			SelectedModel: "llama-3.3-70b",
			ProviderID:    "ollama-local",
			Rationale:     "Airgapped local weight execution when external network egress is prohibited.",
			IsPinned:      globalProviderStore.overrides["offline_fallback"] != "",
		},
	}

	// Apply any active overrides
	for i := range routing {
		if pinned, ok := globalProviderStore.overrides[routing[i].Intent]; ok && pinned != "" {
			routing[i].SelectedModel = pinned
			routing[i].IsPinned = true
			routing[i].Rationale = "Manually pinned by operator."
		}
	}

	writeJSON(w, http.StatusOK, ProviderInventoryResponseDTO{
		Providers:        providers,
		RoutingDecisions: routing,
		LastEvaluatedAt:  now,
	})
}

func (s *Server) handleOverrideRouter(w http.ResponseWriter, r *http.Request) {
	user := s.getAuthenticatedUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", "")
		return
	}

	var env MutationEnvelope[RouterOverridePayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON payload", "")
		return
	}

	payload := env.Payload
	if payload.Intent == "" {
		writeError(w, http.StatusBadRequest, "invalid_intent", "Intent is required", "")
		return
	}

	globalProviderStore.mu.Lock()
	defer globalProviderStore.mu.Unlock()

	if payload.IsPinned && payload.ModelID != "" {
		globalProviderStore.overrides[payload.Intent] = payload.ModelID
	} else {
		delete(globalProviderStore.overrides, payload.Intent)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"intent":    payload.Intent,
		"model_id":  payload.ModelID,
		"is_pinned": payload.IsPinned,
		"status":    "updated",
	})
}
