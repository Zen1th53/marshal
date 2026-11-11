package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
)

// ProviderAuthStatus represents the evidenced authentication state of a provider.
type ProviderAuthStatus string

const (
	AuthStatusSignedIn    ProviderAuthStatus = "SIGNED_IN"
	AuthStatusNotSignedIn ProviderAuthStatus = "NOT_SIGNED_IN"
	AuthStatusSkipped     ProviderAuthStatus = "SKIPPED"
	AuthStatusUnsupported ProviderAuthStatus = "UNSUPPORTED"
	AuthStatusTimeout     ProviderAuthStatus = "TIMEOUT"
	AuthStatusError       ProviderAuthStatus = "ERROR"
	AuthStatusNotProbed   ProviderAuthStatus = "NOT_PROBED"
	AuthStatusStale       ProviderAuthStatus = "STALE"
	AuthStatusUnknown     ProviderAuthStatus = "UNKNOWN"
)

// ProviderAuthObservation carries explicit observed authentication evidence.
// It never conflates discovery or a skipped check with authenticated evidence.
type ProviderAuthObservation struct {
	Provider   string             `json:"provider"`
	Status     ProviderAuthStatus `json:"status"`
	Reason     string             `json:"reason"`
	SessionID  string             `json:"session_id,omitempty"`
	Source     string             `json:"source"`
	ObservedAt time.Time          `json:"observed_at"`
	Freshness  time.Duration      `json:"freshness"`
}

// IsFresh reports whether the auth observation was made within maxAge.
func (o ProviderAuthObservation) IsFresh(maxAge time.Duration, now time.Time) bool {
	if o.ObservedAt.IsZero() {
		return false
	}
	if maxAge <= 0 {
		maxAge = 60 * time.Second
	}
	return now.Sub(o.ObservedAt) <= maxAge
}

// ProviderUsageObservation tracks durable consumption metrics for a provider.
// Nullable total tokens and cost distinguish measured zero from unknown/unreported.
type ProviderUsageObservation struct {
	Reported         bool          `json:"reported"`
	PromptTokens     *int64        `json:"prompt_tokens,omitempty"`
	CompletionTokens *int64        `json:"completion_tokens,omitempty"`
	TotalTokens      *int64        `json:"total_tokens,omitempty"`
	CostUSD          *float64      `json:"cost_usd,omitempty"`
	ModelCalls       int           `json:"model_calls"`
	Duration         time.Duration `json:"duration"`
	SessionID        string        `json:"session_id,omitempty"`
	Source           string        `json:"source"`
	ObservedAt       time.Time     `json:"observed_at"`
}

// ProviderCooldownObservation records active rate limit or backoff cooldown.
type ProviderCooldownObservation struct {
	Active     bool          `json:"active"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	ResetTime  time.Time     `json:"reset_time,omitempty"`
	Source     string        `json:"source"`
	Reason     string        `json:"reason"`
	ObservedAt time.Time     `json:"observed_at"`
}

// ProviderStatusDetail is the canonical combined provider observation.
type ProviderStatusDetail struct {
	Name            string                      `json:"name"`
	Canonical       string                      `json:"canonical"`
	Found           bool                        `json:"found"`
	Path            string                      `json:"path,omitempty"`
	Version         string                      `json:"version,omitempty"`
	Model           string                      `json:"model,omitempty"`
	Auth            ProviderAuthObservation     `json:"auth"`
	Usage           ProviderUsageObservation    `json:"usage"`
	Cooldown        ProviderCooldownObservation `json:"cooldown"`
	QuotaReason     string                      `json:"quota_reason"`
	ResetReason     string                      `json:"reset_reason"`
	AllowanceReason string                      `json:"allowance_reason"`
	ObservedAt      time.Time                   `json:"observed_at"`
}

const (
	defaultAuthProbeTimeout = 2 * time.Second
	authObservationMaxAge   = 60 * time.Second
)

// ProviderStatusTracker manages in-memory observations per runtime instance.
type ProviderStatusTracker struct {
	mu       sync.RWMutex
	auth     map[string]ProviderAuthObservation
	usage    map[string]ProviderUsageObservation
	cooldown map[string]ProviderCooldownObservation
}

func newProviderStatusTracker() *ProviderStatusTracker {
	return &ProviderStatusTracker{
		auth:     make(map[string]ProviderAuthObservation),
		usage:    make(map[string]ProviderUsageObservation),
		cooldown: make(map[string]ProviderCooldownObservation),
	}
}

var (
	trackerMu       sync.RWMutex
	runtimeTrackers = make(map[*Runtime]*ProviderStatusTracker)
	defaultTracker  = newProviderStatusTracker()

	authProbeHookMu     sync.RWMutex
	customAuthProbeHook func(ctx context.Context, provider, binary string) (ProviderAuthObservation, bool)
)

// SetAuthProbeHookForTest overrides the low-level provider auth probe.
func SetAuthProbeHookForTest(fn func(ctx context.Context, provider, binary string) (ProviderAuthObservation, bool)) func() {
	authProbeHookMu.Lock()
	prev := customAuthProbeHook
	customAuthProbeHook = fn
	authProbeHookMu.Unlock()
	return func() {
		authProbeHookMu.Lock()
		customAuthProbeHook = prev
		authProbeHookMu.Unlock()
	}
}

func trackerFor(r *Runtime) *ProviderStatusTracker {
	if r == nil {
		return defaultTracker
	}
	trackerMu.RLock()
	t, ok := runtimeTrackers[r]
	trackerMu.RUnlock()
	if ok {
		return t
	}

	trackerMu.Lock()
	defer trackerMu.Unlock()
	if t, ok = runtimeTrackers[r]; ok {
		return t
	}
	t = newProviderStatusTracker()
	r.restoreProviderObservations(t)
	runtimeTrackers[r] = t
	return t
}

// RecordProviderAuth stores an authoritative authentication observation.
func (r *Runtime) RecordProviderAuth(obs ProviderAuthObservation) {
	if obs.Provider == "" {
		return
	}
	canonical := CanonicalProviderName(obs.Provider)
	obs.Provider = canonical
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = time.Now().UTC()
	}
	t := trackerFor(r)
	t.mu.Lock()
	t.auth[canonical] = obs
	t.mu.Unlock()
}

// RecordProviderUsage stores durable usage for a provider in the runtime session.
func (r *Runtime) RecordProviderUsage(provider string, obs ProviderUsageObservation) error {
	canonical := CanonicalProviderName(provider)
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = time.Now().UTC()
	}
	t := trackerFor(r)
	t.mu.Lock()
	t.usage[canonical] = obs
	t.mu.Unlock()
	return r.persistProviderObservation("provider.usage", canonical, obs, obs.ObservedAt)
}

// RecordProviderCooldown stores rate-limit cooldown for a provider.
func (r *Runtime) RecordProviderCooldown(provider string, obs ProviderCooldownObservation) error {
	canonical := CanonicalProviderName(provider)
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = time.Now().UTC()
	}
	t := trackerFor(r)
	t.mu.Lock()
	if until := obs.ObservedAt.Add(obs.RetryAfter); obs.RetryAfter > 0 && until.After(obs.ResetTime) {
		obs.ResetTime = until
	}
	t.cooldown[canonical] = obs
	t.mu.Unlock()
	return r.persistProviderObservation("provider.cooldown", canonical, obs, obs.ObservedAt)
}

// CanonicalProviderName standardizes aliases to canonical provider IDs.
func CanonicalProviderName(provider string) string {
	lower := strings.ToLower(strings.TrimSpace(provider))
	switch lower {
	case "antigravity", "agy":
		return "agy"
	case "codex":
		return "codex"
	case "claude":
		return "claude"
	case "opencode":
		return "opencode"
	default:
		return lower
	}
}

// ProviderDisplayName maps canonical IDs to operator display labels.
func ProviderDisplayName(provider string) string {
	switch CanonicalProviderName(provider) {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	case "opencode":
		return "OpenCode"
	case "agy":
		return "Antigravity"
	default:
		name := strings.TrimSpace(provider)
		if len(name) > 0 {
			return strings.ToUpper(name[:1]) + name[1:]
		}
		return "Provider"
	}
}

// ProviderStatusDetails returns canonical provider telemetry and observation evidence.
// It never launches turns, scrapes credentials, or executes side-effectful commands.
func (r *Runtime) ProviderStatusDetails(ctx context.Context) ([]ProviderStatusDetail, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	discovered, err := r.ProviderObservations(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	tracker := trackerFor(r)
	details := make([]ProviderStatusDetail, 0, len(discovered))

	for _, d := range discovered {
		canonical := CanonicalProviderName(d.Name)
		displayName := ProviderDisplayName(canonical)

		detail := ProviderStatusDetail{
			Name:            displayName,
			Canonical:       canonical,
			Found:           d.Found,
			Path:            d.Path,
			ObservedAt:      now,
			QuotaReason:     "quota and usage limits are read from the provider CLI's own local report where it writes one",
			ResetReason:     "limit reset times are read from the provider CLI's own local report where it writes one",
			AllowanceReason: "no provider exposes a machine-readable temporary or promotional allowance through a supported interface",
		}

		// Auth observation: check recorded observation first, else run read-only check if supported.
		detail.Auth = r.getOrProbeAuth(ctx, canonical, d.Found, d.Path, now, tracker)

		// Usage observation: check recorded usage for this provider/session.
		detail.Usage = r.getUsageObservation(canonical, tracker, now)

		// Cooldown observation: check recorded rate limit cooldown.
		detail.Cooldown = r.getCooldownObservation(canonical, tracker, now)

		details = append(details, detail)
	}

	return details, nil
}

// ProviderStatusDetail returns the canonical detail for one provider.
func (r *Runtime) ProviderStatusDetail(ctx context.Context, provider string) (*ProviderStatusDetail, error) {
	details, err := r.ProviderStatusDetails(ctx)
	if err != nil {
		return nil, err
	}
	canonical := CanonicalProviderName(provider)
	for _, d := range details {
		if d.Canonical == canonical || strings.EqualFold(d.Name, provider) {
			return &d, nil
		}
	}
	return nil, fmt.Errorf("provider %q not found in discovery", provider)
}

func (r *Runtime) getOrProbeAuth(ctx context.Context, canonical string, found bool, path string, now time.Time, tracker *ProviderStatusTracker) ProviderAuthObservation {
	tracker.mu.RLock()
	cached, hasCached := tracker.auth[canonical]
	tracker.mu.RUnlock()

	if hasCached {
		if cached.IsFresh(authObservationMaxAge, now) {
			cached.Freshness = now.Sub(cached.ObservedAt)
			return cached
		}
		// Stale cached observation: must report STALE/UNKNOWN until reprobed.
		return ProviderAuthObservation{
			Provider:   canonical,
			Status:     AuthStatusStale,
			Reason:     "cached authentication observation is stale; reprobe required",
			Source:     cached.Source,
			ObservedAt: cached.ObservedAt,
			Freshness:  now.Sub(cached.ObservedAt),
		}
	}

	if !found {
		return ProviderAuthObservation{
			Provider:   canonical,
			Status:     AuthStatusNotProbed,
			Reason:     "provider executable was not found on PATH",
			Source:     "internal/app/provider_status.go",
			ObservedAt: now,
		}
	}

	// Check if a test hook handles this probe
	authProbeHookMu.RLock()
	hook := customAuthProbeHook
	authProbeHookMu.RUnlock()
	if hook != nil {
		if obs, ok := hook(ctx, canonical, path); ok {
			if obs.ObservedAt.IsZero() {
				obs.ObservedAt = now
			}
			tracker.mu.Lock()
			tracker.auth[canonical] = obs
			tracker.mu.Unlock()
			return obs
		}
	}

	// Bounded, side-effect-free probe where supported.
	switch canonical {
	case "codex":
		obs := probeCodexAuthReadOnly(ctx, path, now)
		tracker.mu.Lock()
		tracker.auth[canonical] = obs
		tracker.mu.Unlock()
		return obs
	case "claude":
		return ProviderAuthObservation{
			Provider:   "claude",
			Status:     AuthStatusUnsupported,
			Reason:     "claude auth status mutates ~/.claude.json on disk; read-only auth inspection is not supported",
			Source:     "internal/app/provider_status.go",
			ObservedAt: now,
		}
	case "opencode":
		return ProviderAuthObservation{
			Provider:   "opencode",
			Status:     AuthStatusUnsupported,
			Reason:     "opencode has no read-only auth status CLI; inspection is not supported",
			Source:     "internal/app/provider_status.go",
			ObservedAt: now,
		}
	case "agy":
		return ProviderAuthObservation{
			Provider:   "agy",
			Status:     AuthStatusUnsupported,
			Reason:     "antigravity has no read-only auth status CLI; sign-in is managed via application",
			Source:     "internal/app/provider_status.go",
			ObservedAt: now,
		}
	default:
		return ProviderAuthObservation{
			Provider:   canonical,
			Status:     AuthStatusUnsupported,
			Reason:     fmt.Sprintf("%s does not expose a supported read-only auth inspection interface", canonical),
			Source:     "internal/app/provider_status.go",
			ObservedAt: now,
		}
	}
}

func probeCodexAuthReadOnly(ctx context.Context, binary string, now time.Time) ProviderAuthObservation {
	if binary == "" {
		resolved, err := project.FindBinary("codex")
		if err != nil {
			return ProviderAuthObservation{
				Provider:   "codex",
				Status:     AuthStatusNotProbed,
				Reason:     "codex executable was not found on PATH",
				Source:     "internal/app/provider_status.go",
				ObservedAt: now,
			}
		}
		binary = resolved
	}

	probeCtx, cancel := context.WithTimeout(ctx, defaultAuthProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, binary, "login", "status")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 1 {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 50 * time.Millisecond
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

	out, err := cmd.CombinedOutput()
	text := string(out)

	if err == nil {
		return ProviderAuthObservation{
			Provider:   "codex",
			Status:     AuthStatusSignedIn,
			Reason:     "codex reported signed in",
			Source:     "codex login status",
			ObservedAt: now,
		}
	}

	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return ProviderAuthObservation{
			Provider:   "codex",
			Status:     AuthStatusTimeout,
			Reason:     "codex auth status probe timed out after 2s",
			Source:     "codex login status",
			ObservedAt: now,
		}
	}

	lower := strings.ToLower(text)
	if strings.Contains(lower, "not logged in") || strings.Contains(lower, "not signed in") ||
		strings.Contains(lower, "invalid api key") || strings.Contains(lower, "sign in with chatgpt") {
		return ProviderAuthObservation{
			Provider:   "codex",
			Status:     AuthStatusNotSignedIn,
			Reason:     "Codex is not signed in - run `codex login`",
			Source:     "codex login status",
			ObservedAt: now,
		}
	}

	for _, marker := range []string{"unrecognized subcommand", "unknown subcommand", "unknown command", "unexpected argument 'login'"} {
		if strings.Contains(lower, marker) {
			return ProviderAuthObservation{
				Provider:   "codex",
				Status:     AuthStatusUnsupported,
				Reason:     "codex executable does not support login status subcommand",
				Source:     "codex login status",
				ObservedAt: now,
			}
		}
	}

	return ProviderAuthObservation{
		Provider:   "codex",
		Status:     AuthStatusError,
		Reason:     strings.TrimSpace(text),
		Source:     "codex login status",
		ObservedAt: now,
	}
}

func (r *Runtime) getUsageObservation(canonical string, tracker *ProviderStatusTracker, now time.Time) ProviderUsageObservation {
	tracker.mu.RLock()
	usage, ok := tracker.usage[canonical]
	tracker.mu.RUnlock()
	if ok {
		return usage
	}

	return ProviderUsageObservation{
		Reported:   false,
		Source:     "internal/app/provider_status.go",
		ObservedAt: now,
	}
}

func (r *Runtime) getCooldownObservation(canonical string, tracker *ProviderStatusTracker, now time.Time) ProviderCooldownObservation {
	tracker.mu.RLock()
	cd, ok := tracker.cooldown[canonical]
	tracker.mu.RUnlock()
	if ok {
		if cd.Active && !cd.ResetTime.IsZero() && now.After(cd.ResetTime) {
			cd.Active = false
			cd.Reason = "cooldown expired"
		}
		return cd
	}

	return ProviderCooldownObservation{
		Active:     false,
		Reason:     "no active rate limit cooldown recorded",
		Source:     "internal/app/provider_status.go",
		ObservedAt: now,
	}
}

// Provider observations are persisted in the canonical audit history, keeping
// restart readback independent of process-local tracker caches.
func (r *Runtime) persistProviderObservation(kind, provider string, observation any, at time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		return err
	}
	data["provider"] = provider
	id, err := model.NewID("PROVIDER-OBS-")
	if err != nil {
		return err
	}
	return r.store.AppendEvent(context.Background(), nil, model.Event{ID: id, Type: kind, Timestamp: at, Data: data})
}

func (r *Runtime) restoreProviderObservations(t *ProviderStatusTracker) {
	if r == nil || r.store == nil {
		return
	}
	observations, err := r.store.ListEvents(context.Background())
	if err != nil {
		return
	}
	for _, event := range observations {
		if event.Type != "provider.usage" && event.Type != "provider.cooldown" {
			continue
		}
		provider, _ := event.Data["provider"].(string)
		if provider == "" {
			continue
		}
		raw, err := json.Marshal(event.Data)
		if err != nil {
			continue
		}
		if event.Type == "provider.usage" {
			var obs ProviderUsageObservation
			if json.Unmarshal(raw, &obs) == nil {
				t.usage[provider] = obs
			}
		} else {
			var obs ProviderCooldownObservation
			if json.Unmarshal(raw, &obs) == nil {
				t.cooldown[provider] = obs
			}
		}
	}
}

func (r *Runtime) observeProviderResult(provider string, result adapter.Result, duration time.Duration) error {
	usage := result.Usage
	calls := usage.ModelCalls
	if calls == 0 {
		calls = 1
	}
	if usage.DurationMs > 0 {
		duration = time.Duration(usage.DurationMs) * time.Millisecond
	}
	obs := ProviderUsageObservation{Reported: usage.Reported, PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens, CostUSD: usage.CostUSD, ModelCalls: calls, Duration: duration, SessionID: result.SessionID, Source: "canonical provider execution", ObservedAt: time.Now().UTC()}
	err := r.RecordProviderUsage(provider, obs)
	for _, event := range result.Events {
		if cooldown, ok := providerRateLimitObservation(event, obs.ObservedAt); ok {
			err = errors.Join(err, r.RecordProviderCooldown(provider, cooldown))
		}
	}
	return err
}

// Only structured provider signals establish a cooldown. Ordinary failures and
// prose in stderr are not evidence of throttling or a known reset time.
func providerRateLimitObservation(event map[string]any, now time.Time) (ProviderCooldownObservation, bool) {
	kind, _ := event["type"].(string)
	code, _ := event["code"].(string)
	status := fmt.Sprint(event["http_status"])
	if status == "<nil>" {
		status = fmt.Sprint(event["status_code"])
	}
	if nested, ok := event["error"].(map[string]any); ok {
		if nestedKind, _ := nested["type"].(string); nestedKind == "rate_limit_error" {
			kind = nestedKind
		}
		if nestedCode, _ := nested["code"].(string); nestedCode == "rate_limit_exceeded" {
			code = nestedCode
		}
	}
	if kind != "rate_limit" && kind != "rate_limit_error" && code != "rate_limit_exceeded" && status != "429" {
		return ProviderCooldownObservation{}, false
	}
	obs := ProviderCooldownObservation{Active: true, Source: "structured provider rate-limit event", Reason: "provider reported rate limit", ObservedAt: now}
	if retry, ok := event["retry_after"]; ok {
		obs.RetryAfter, _ = execution.ParseRetryAfter(fmt.Sprint(retry), now)
	}
	if reset, ok := event["reset_time"].(string); ok {
		obs.ResetTime, _ = time.Parse(time.RFC3339Nano, reset)
	}
	return obs, true
}
