package webcontrol

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/secrets"
)

type ActiveModelDTO struct {
	ID            string  `json:"id"`
	ContextWindow int     `json:"context_window"`
	LatencyP95Ms  float64 `json:"latency_p95_ms"`
}

type SecretRefMetadataDTO struct {
	Configured bool   `json:"configured"`
	RefName    string `json:"ref_name"`
	Provider   string `json:"provider"`
	Version    string `json:"version"`
}

type ProviderDTO struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Class        string               `json:"class"`        // "cloud", "local"
	Enabled      bool                 `json:"enabled"`
	EndpointURL  string               `json:"endpoint_url,omitempty"`
	ProbeStatus  string               `json:"probe_status"` // "healthy", "degraded", "unavailable", "not_run"
	Capabilities []string             `json:"capabilities"`
	Models       []ActiveModelDTO     `json:"models"`
	LastProbedAt time.Time            `json:"last_probed_at"`
	SecretRef    SecretRefMetadataDTO `json:"secret_ref"`
}

type RouterDecisionDTO struct {
	Intent        string `json:"intent"` // "planning", "implementation", "security_audit", "offline_fallback"
	SelectedModel string `json:"selected_model"`
	ProviderID    string `json:"provider_id"`
	Rationale     string `json:"rationale"`
	IsPinned      bool   `json:"is_pinned"`
}

type ProviderInventoryResponseDTO struct {
	Providers        []ProviderDTO       `json:"providers"`
	RoutingDecisions []RouterDecisionDTO `json:"routing_decisions"`
	LastEvaluatedAt  time.Time           `json:"last_evaluated_at"`
}

type RouterOverridePayload struct {
	Intent   string `json:"intent"`
	ModelID  string `json:"model_id"`
	IsPinned bool   `json:"is_pinned"`
}

type UpdateProviderPayload struct {
	Enabled     *bool    `json:"enabled,omitempty"`
	EndpointURL *string  `json:"endpoint_url,omitempty"`
	Models      []string `json:"models,omitempty"`
}

type SetProviderSecretPayload struct {
	SecretKey string `json:"secret_key,omitempty"`
	EnvVar    string `json:"env_var,omitempty"`
	Version   string `json:"version,omitempty"`
}

type ProviderConfig struct {
	ID           string
	Name         string
	Class        string
	Enabled      bool
	EndpointURL  string
	ProbeStatus  string
	Capabilities []string
	Models       []ActiveModelDTO
	LastProbedAt time.Time
	SecretRef    secrets.Ref
	HasSecret    bool
}

type ProviderStore struct {
	mu        sync.RWMutex
	providers map[string]*ProviderConfig
	overrides map[string]string // intent -> pinned model ID
}

var globalProviderStore = newProviderStore()

func newProviderStore() *ProviderStore {
	now := time.Now().UTC()
	store := &ProviderStore{
		providers: make(map[string]*ProviderConfig),
		overrides: make(map[string]string),
	}

	initial := []*ProviderConfig{
		{
			ID:          "anthropic",
			Name:        "Anthropic Claude",
			Class:       "cloud",
			Enabled:     true,
			ProbeStatus: "healthy",
			Capabilities: []string{"reasoning", "tool_use", "code_generation", "json_mode"},
			Models: []ActiveModelDTO{
				{ID: "claude-3-7-sonnet", ContextWindow: 200000, LatencyP95Ms: 850.0},
				{ID: "claude-3-5-haiku", ContextWindow: 200000, LatencyP95Ms: 280.0},
			},
			LastProbedAt: now.Add(-2 * time.Minute),
			SecretRef: secrets.Ref{
				Provider: "env",
				Name:     "sec-anthropic-auth",
				Version:  "1",
			},
			HasSecret: os.Getenv("ANTHROPIC_API_KEY") != "",
		},
		{
			ID:          "google",
			Name:        "Google Gemini",
			Class:       "cloud",
			Enabled:     true,
			ProbeStatus: "healthy",
			Capabilities: []string{"multimodal", "1m_context", "code_generation", "tool_use"},
			Models: []ActiveModelDTO{
				{ID: "gemini-2.0-pro-exp", ContextWindow: 1000000, LatencyP95Ms: 620.0},
				{ID: "gemini-2.0-flash", ContextWindow: 1000000, LatencyP95Ms: 210.0},
			},
			LastProbedAt: now.Add(-3 * time.Minute),
			SecretRef: secrets.Ref{
				Provider: "env",
				Name:     "sec-gemini-auth",
				Version:  "1",
			},
			HasSecret: os.Getenv("GEMINI_API_KEY") != "",
		},
		{
			ID:          "openai",
			Name:        "OpenAI Platform",
			Class:       "cloud",
			Enabled:     true,
			ProbeStatus: "healthy",
			Capabilities: []string{"code_generation", "structured_outputs", "tool_use"},
			Models: []ActiveModelDTO{
				{ID: "gpt-4o", ContextWindow: 128000, LatencyP95Ms: 540.0},
				{ID: "gpt-4o-mini", ContextWindow: 128000, LatencyP95Ms: 190.0},
			},
			LastProbedAt: now.Add(-1 * time.Minute),
			SecretRef: secrets.Ref{
				Provider: "env",
				Name:     "sec-openai-auth",
				Version:  "1",
			},
			HasSecret: os.Getenv("OPENAI_API_KEY") != "",
		},
		{
			ID:          "opencode",
			Name:        "OpenCode Protocol Agent",
			Class:       "local",
			Enabled:     true,
			ProbeStatus: "healthy",
			Capabilities: []string{"local_weights", "code_edit", "sandbox_run"},
			Models: []ActiveModelDTO{
				{ID: "deepseek-r1-qwen", ContextWindow: 65536, LatencyP95Ms: 450.0},
				{ID: "qwen2.5-coder-7b", ContextWindow: 32768, LatencyP95Ms: 180.0},
			},
			LastProbedAt: now.Add(-4 * time.Minute),
			SecretRef: secrets.Ref{
				Provider: "env",
				Name:     "sec-opencode-auth",
				Version:  "1",
			},
			HasSecret: true,
		},
		{
			ID:          "ollama-local",
			Name:        "Local Ollama Engine",
			Class:       "local",
			Enabled:     true,
			EndpointURL: "http://127.0.0.1:11434",
			ProbeStatus: "healthy",
			Capabilities: []string{"offline_airgap", "code_generation"},
			Models: []ActiveModelDTO{
				{ID: "llama-3.3-70b", ContextWindow: 32768, LatencyP95Ms: 1400.0},
				{ID: "qwen2.5-coder-32b", ContextWindow: 32768, LatencyP95Ms: 920.0},
			},
			LastProbedAt: now.Add(-5 * time.Minute),
			SecretRef: secrets.Ref{
				Provider: "env",
				Name:     "sec-ollama-auth",
				Version:  "1",
			},
			HasSecret: true,
		},
	}

	for _, p := range initial {
		store.providers[p.ID] = p
	}

	return store
}

func (s *Server) handleGetProviders(w http.ResponseWriter, r *http.Request) {
	globalProviderStore.mu.RLock()
	defer globalProviderStore.mu.RUnlock()

	now := time.Now().UTC()

	orderedKeys := []string{"anthropic", "google", "openai", "opencode", "ollama-local"}
	var providers []ProviderDTO

	for _, k := range orderedKeys {
		p, ok := globalProviderStore.providers[k]
		if !ok {
			continue
		}
		providers = append(providers, ProviderDTO{
			ID:           p.ID,
			Name:         p.Name,
			Class:        p.Class,
			Enabled:      p.Enabled,
			EndpointURL:  p.EndpointURL,
			ProbeStatus:  p.ProbeStatus,
			Capabilities: p.Capabilities,
			Models:       p.Models,
			LastProbedAt: p.LastProbedAt,
			SecretRef: SecretRefMetadataDTO{
				Configured: p.HasSecret,
				RefName:    p.SecretRef.Name,
				Provider:   p.SecretRef.Provider,
				Version:    p.SecretRef.Version,
			},
		})
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

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Provider ID is required", "")
		return
	}

	var env MutationEnvelope[UpdateProviderPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON payload: "+err.Error(), "")
		return
	}
	payload := env.Payload

	globalProviderStore.mu.Lock()
	defer globalProviderStore.mu.Unlock()

	p, ok := globalProviderStore.providers[id]
	if !ok {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider not found: "+id, "")
		return
	}

	if payload.Enabled != nil {
		p.Enabled = *payload.Enabled
	}
	if payload.EndpointURL != nil {
		p.EndpointURL = strings.TrimSpace(*payload.EndpointURL)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":           p.ID,
		"enabled":      p.Enabled,
		"endpoint_url": p.EndpointURL,
		"status":       "updated",
	})
}

func (s *Server) handleSetProviderSecret(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Provider ID is required", "")
		return
	}

	var env MutationEnvelope[SetProviderSecretPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON payload: "+err.Error(), "")
		return
	}
	payload := env.Payload

	globalProviderStore.mu.Lock()
	defer globalProviderStore.mu.Unlock()

	p, ok := globalProviderStore.providers[id]
	if !ok {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider not found: "+id, "")
		return
	}

	// SecretRef update: never store or return plaintext in responses.
	// If secret_key is supplied directly in write-only mode, simulate env ref binding.
	envVar := strings.TrimSpace(payload.EnvVar)
	if envVar == "" {
		envVar = p.SecretRef.Name
	}
	version := strings.TrimSpace(payload.Version)
	if version == "" {
		version = "1"
	}

	p.SecretRef = secrets.Ref{
		Provider: "env",
		Name:     envVar,
		Version:  version,
	}
	if strings.TrimSpace(payload.SecretKey) != "" || strings.TrimSpace(payload.EnvVar) != "" {
		p.HasSecret = true
	}

	// Strictly write-only response: zero secret material returned
	writeJSON(w, http.StatusOK, map[string]any{
		"provider_id": p.ID,
		"status":      "secret_ref_configured",
		"secret_ref": SecretRefMetadataDTO{
			Configured: p.HasSecret,
			RefName:    p.SecretRef.Name,
			Provider:   p.SecretRef.Provider,
			Version:    p.SecretRef.Version,
		},
	})
}

func (s *Server) handleProbeProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Provider ID is required", "")
		return
	}

	globalProviderStore.mu.Lock()
	defer globalProviderStore.mu.Unlock()

	p, ok := globalProviderStore.providers[id]
	if !ok {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider not found: "+id, "")
		return
	}

	p.LastProbedAt = time.Now().UTC()
	if p.Enabled {
		p.ProbeStatus = "healthy"
	} else {
		p.ProbeStatus = "unavailable"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":             p.ID,
		"probe_status":   p.ProbeStatus,
		"last_probed_at": p.LastProbedAt,
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
