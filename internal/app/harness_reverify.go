package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

const (
	// harnessReverifyMargin is how close to expiry a verified profile must be
	// before the runtime probes it again. The probe renews its 24-hour expiry.
	harnessReverifyMargin = 2 * time.Hour
	// harnessReverifyRetryInterval spaces automatic retries after a failed
	// probe, so a provider that is down is not probed on every scan.
	harnessReverifyRetryInterval = 30 * time.Minute
	// harnessReverifyScanInterval is how often the runtime looks for profiles
	// that entered the re-verify margin.
	harnessReverifyScanInterval = 15 * time.Minute
)

// harnessReverifyState is the in-memory outcome of the newest attempt for one
// provider. It never changes a profile; the probe path does that.
type harnessReverifyState struct {
	InFlight    bool
	LastAttempt time.Time
	LastFailed  bool
}

// harnessReverifyCandidate names the exact provider and model a due profile
// was verified with, so the re-probe repeats the same pair.
type harnessReverifyCandidate struct {
	Harness  string
	Provider string
	Model    string
	Native   bool
}

// harnessProfilesDueForReverify is the clock-injected scheduling decision. It
// returns only verified profiles that are inside the expiry margin, not in
// limit cooldown, not already being probed and not retried too soon after a
// failure. A provider appears at most once.
func harnessProfilesDueForReverify(profiles []model.HarnessProfile, states map[string]harnessReverifyState, cooling map[string]bool, now time.Time) []harnessReverifyCandidate {
	now = now.UTC()
	var due []harnessReverifyCandidate
	seen := make(map[string]bool)
	for _, profile := range profiles {
		provider := harnessReverifyProvider(profile.Harness)
		if seen[provider] {
			continue
		}
		if profile.ProbeEvidenceID == "" || profile.ProbedAt.IsZero() || profile.ExpiresAt.IsZero() {
			continue
		}
		if profile.Validate() != nil || profile.ProbedAt.After(now) {
			continue
		}
		// Only a profile that is still valid but close to expiry is due. An
		// already expired profile is left for the owner, exactly as before.
		if !now.Before(profile.ExpiresAt) || profile.ExpiresAt.Sub(now) > harnessReverifyMargin {
			continue
		}
		if !harnessReverifyProviderSupported(provider) {
			continue
		}
		modelName := strings.TrimSpace(profile.DefaultModel)
		if modelName == "" {
			continue
		}
		if cooling[provider] {
			continue
		}
		state := states[provider]
		if state.InFlight {
			continue
		}
		if state.LastFailed && !state.LastAttempt.IsZero() && now.Sub(state.LastAttempt) < harnessReverifyRetryInterval {
			continue
		}
		seen[provider] = true
		due = append(due, harnessReverifyCandidate{
			Harness:  profile.Harness,
			Provider: provider,
			Model:    modelName,
			Native:   harnessReverifyNative(profile),
		})
	}
	return due
}

// harnessReverifyProvider reverses marshalHarnessName for the profiles the
// runtime probes.
func harnessReverifyProvider(harness string) string {
	switch harness {
	case "claude-code":
		return "claude"
	case "antigravity":
		return "agy"
	default:
		return harness
	}
}

func harnessReverifyProviderSupported(provider string) bool {
	switch provider {
	case "codex", "claude", "opencode", "agy":
		return true
	default:
		return false
	}
}

func harnessReverifyNative(profile model.HarnessProfile) bool {
	return harnessReverifyProvider(profile.Harness) == "agy" && profile.FeatureSupport["sandbox"] == model.StatusUnsupported && profile.FeatureSupport["host-execution"] != model.StatusNative
}

type harnessReverifier struct {
	runtime  *Runtime
	now      func() time.Time
	probe    func(context.Context, harnessReverifyCandidate) (model.HarnessProfile, error)
	activity func(string)

	mu     sync.Mutex
	states map[string]harnessReverifyState
	scan   sync.Mutex
}

func newHarnessReverifier(r *Runtime) *harnessReverifier {
	return &harnessReverifier{
		runtime: r,
		now:     time.Now,
		probe:   r.reverifyHarnessProfile,
		states:  make(map[string]harnessReverifyState),
	}
}

// startHarnessReverifyLoop starts the small runner from the runtime's
// long-lived background lifecycle. Only the lifecycle owner probes.
func (r *Runtime) startHarnessReverifyLoop(ctx context.Context) {
	if r == nil || r.store == nil {
		return
	}
	r.startAgySigninKeepaliveLoop(ctx)
	reverifier := newHarnessReverifier(r)
	go func() {
		ticker := time.NewTicker(harnessReverifyScanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !r.IsLifecycleOwner() {
					return
				}
				reverifier.runOnce(ctx)
			}
		}
	}()
}

func (h *harnessReverifier) runOnce(ctx context.Context) {
	if h == nil || h.runtime == nil || h.runtime.store == nil {
		return
	}
	h.scan.Lock()
	defer h.scan.Unlock()

	now := h.now().UTC()
	profiles, err := h.profiles(ctx)
	if err != nil {
		return
	}
	cooling := make(map[string]bool)
	for _, profile := range profiles {
		provider := harnessReverifyProvider(profile.Harness)
		if _, limited := providerHealth.Cooling(provider, now); limited {
			cooling[provider] = true
		}
	}
	for _, candidate := range harnessProfilesDueForReverify(profiles, h.snapshot(), cooling, now) {
		if ctx.Err() != nil {
			return
		}
		if _, limited := providerHealth.Cooling(candidate.Provider, h.now().UTC()); limited {
			continue
		}
		h.markAttempt(candidate.Provider, now)
		profile, probeErr := h.probe(ctx, candidate)
		if probeErr != nil {
			h.runtime.recordHarnessVerifyFailure(context.WithoutCancel(ctx), candidate.Provider, candidate.Model, probeErr)
			h.markResult(candidate.Provider, true)
			h.recordActivity(fmt.Sprintf("Harness re-verify failed for %s %s: %v", candidate.Provider, candidate.Model, probeErr))
			continue
		}
		h.markResult(candidate.Provider, false)
		h.recordActivity(fmt.Sprintf("Harness re-verified %s %s; evidence %s", candidate.Provider, candidate.Model, profile.ProbeEvidenceID))
	}
}

func (h *harnessReverifier) profiles(ctx context.Context) ([]model.HarnessProfile, error) {
	overview, err := h.runtime.store.ProviderOverview(ctx, h.runtime.ProjectID(), h.now().UTC())
	if err != nil {
		return nil, err
	}
	var profiles []model.HarnessProfile
	for _, status := range overview {
		profile, err := h.runtime.store.GetHarnessProfile(ctx, status.Provider)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if profile != nil {
			profiles = append(profiles, *profile)
		}
	}
	return profiles, nil
}

func (h *harnessReverifier) snapshot() map[string]harnessReverifyState {
	h.mu.Lock()
	defer h.mu.Unlock()
	states := make(map[string]harnessReverifyState, len(h.states))
	for provider, state := range h.states {
		states[provider] = state
	}
	return states
}

func (h *harnessReverifier) markAttempt(provider string, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.states[provider]
	state.InFlight = true
	state.LastAttempt = now
	h.states[provider] = state
}

func (h *harnessReverifier) markResult(provider string, failed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.states[provider]
	state.InFlight = false
	state.LastFailed = failed
	if failed {
		state.LastAttempt = h.now().UTC()
	}
	h.states[provider] = state
}

func (h *harnessReverifier) recordActivity(message string) {
	if h.activity != nil {
		h.activity(message)
		return
	}
	if h.runtime == nil {
		return
	}
	if service := h.runtime.Marshal(); service != nil && service.RecordActivity != nil {
		service.RecordActivity(message)
	}
}

// reverifyHarnessProfile calls the existing probe path. Native agy keeps its
// separate native contract; every other provider repeats the governed probe.
func (r *Runtime) reverifyHarnessProfile(ctx context.Context, candidate harnessReverifyCandidate) (model.HarnessProfile, error) {
	if candidate.Native {
		return r.VerifyNativeAgyWorker(ctx, candidate.Model)
	}
	return r.verifyMarshalWorker(ctx, candidate.Provider, candidate.Model)
}
