package usage

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// agy writes no quota figures locally and its CLI reports none, but it drops
// a model family from `agy models` while that family's quota is spent (the
// Gemini 4 family vanished, Flash, Claude and GPT-OSS stayed). The offer is
// therefore the most exact thing that can be said: which families can run
// now, and which ones that this account had before are gone.

// ModelGroup is one family of models agy offers.
type ModelGroup struct {
	Name    string
	Offered bool
}

// agyOfferEvery bounds how often `agy models` runs.
const agyOfferEvery = 2 * time.Minute

var agyOffer struct {
	sync.Mutex
	at     time.Time
	groups []string
	err    bool
}

// agyModelsLister runs `agy models`; tests replace it.
var agyModelsLister = func(ctx context.Context) ([]byte, error) {
	path, err := exec.LookPath("agy")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, "models")
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1")
	return cmd.Output()
}

// agyGroup names the family of a model ID.
func agyGroup(id string) string {
	id = strings.ToLower(id)
	switch {
	case strings.Contains(id, "orchestrator"):
		return "Orchestrator"
	case strings.Contains(id, "flash"):
		return "Flash"
	case strings.HasPrefix(id, "gemini-") && len(id) > 7 && id[7] >= '0' && id[7] <= '9':
		// gemini-4-carbon-high → "Gemini 4": a generation is one family.
		return "Gemini " + strings.SplitN(strings.TrimPrefix(id, "gemini-"), "-", 2)[0]
	case strings.HasPrefix(id, "claude"):
		return "Claude"
	case strings.HasPrefix(id, "gpt"):
		return "GPT-OSS"
	case strings.HasPrefix(id, "gemini"):
		return "Gemini"
	}
	return ""
}

// agyOfferedGroups lists the families `agy models` offers now.
func agyOfferedGroups(now time.Time) ([]string, bool) {
	agyOffer.Lock()
	defer agyOffer.Unlock()
	if !agyOffer.at.IsZero() && now.Sub(agyOffer.at) < agyOfferEvery {
		return slices.Clone(agyOffer.groups), !agyOffer.err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := agyModelsLister(ctx)
	agyOffer.at = now
	if err != nil {
		agyOffer.groups, agyOffer.err = nil, true
		return nil, false
	}
	var groups []string
	for _, line := range strings.Split(string(out), "\n") {
		id, _, _ := strings.Cut(strings.TrimSpace(line), "\t")
		if id == "" || strings.HasPrefix(id, "Fetching") {
			continue
		}
		if g := agyGroup(id); g != "" && !slices.Contains(groups, g) {
			groups = append(groups, g)
		}
	}
	agyOffer.groups, agyOffer.err = groups, false
	return slices.Clone(groups), true
}

// AgySeenGroupsFile keeps every family this account was ever offered, so a
// family that disappears is reported as spent rather than forgotten.
func AgySeenGroupsFile() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "marshal", "agy-model-groups.json")
}

func readAgy(now time.Time, seenFile string) Quota {
	q := Quota{Provider: "agy"}
	offered, ok := agyOfferedGroups(now)
	if !ok {
		q.Reason = NotExposed
		return q
	}
	var seen []string
	if seenFile != "" {
		if data, err := os.ReadFile(seenFile); err == nil {
			_ = json.Unmarshal(data, &seen)
		}
	}
	changed := false
	for _, g := range offered {
		if !slices.Contains(seen, g) {
			seen, changed = append(seen, g), true
		}
	}
	if changed && seenFile != "" && os.MkdirAll(filepath.Dir(seenFile), 0o700) == nil {
		if data, err := json.Marshal(seen); err == nil {
			_ = os.WriteFile(seenFile, data, 0o600)
		}
	}
	for _, g := range seen {
		q.Offer = append(q.Offer, ModelGroup{Name: g, Offered: slices.Contains(offered, g)})
	}
	q.AsOf = now
	return q
}

// OfferedNone reports whether a provider with a model offer can run nothing.
func (q Quota) OfferedNone() bool {
	if len(q.Offer) == 0 {
		return false
	}
	for _, g := range q.Offer {
		if g.Offered {
			return false
		}
	}
	return true
}

// resetAgyOffer forgets the cached offer; tests use it.
func resetAgyOffer() {
	agyOffer.Lock()
	agyOffer.at, agyOffer.groups, agyOffer.err = time.Time{}, nil, false
	agyOffer.Unlock()
}
