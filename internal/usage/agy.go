package usage

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
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

// agyUsageLister runs `agy -p /usage`, which prints one tab-separated record
// per quota window: group, window, percent left, reset time (UTC when not
// printed to a terminal). Tests replace it.
var agyUsageLister = func(ctx context.Context) ([]byte, error) {
	path, err := exec.LookPath("agy")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, "-p", "/usage")
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1")
	// Both streams: a signed-out agy asks for authentication on either and
	// then waits until the bound; what it printed is still returned.
	return cmd.CombinedOutput()
}

// parseAgyUsage reads agy's /usage records into windows labelled by group
// and window ("Gemini 5h", "Claude and GPT wk", "Gemini 4 Argon wk").
func parseAgyUsage(out []byte, now time.Time) []Window {
	var windows []Window
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) != 4 {
			continue
		}
		pct, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(f[2]), "%"), 64)
		if err != nil || pct < 0 || pct > 100 {
			continue
		}
		resets, err := time.Parse(time.RFC3339, strings.TrimSpace(f[3]))
		if err != nil {
			continue
		}
		group := strings.TrimSuffix(strings.TrimSpace(f[0]), " Models")
		group = strings.TrimSuffix(group, " models")
		kind := "wk"
		if strings.Contains(strings.ToLower(f[1]), "five hour") {
			kind = "5h"
		}
		w := Window{Label: group + " " + kind, LeftPercent: pct}
		if resets.After(now) {
			w.ResetsAt = resets
		}
		windows = append(windows, w)
	}
	return windows
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
	// Tests that count every provider call turn the background listing off,
	// as they turn off the update check.
	if os.Getenv("MARSHAL_NO_USAGE_PROBES") != "" {
		q.Reason = NotExposed
		return q
	}
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
	// The figures agy itself reports, when it answers: every family's
	// 5-hour and weekly percent left and reset time. A signed-out agy has
	// none: it is not signed in, never at its limit.
	windows, signedOut := agyUsageWindows(now)
	if signedOut {
		q.Reason = ReasonNotSignedIn
		return q
	}
	if len(windows) > 0 {
		q.Windows = windows
		for _, w := range windows {
			if w.Known() {
				q.Known = true
			}
		}
	}
	return q
}

var agyUsage struct {
	sync.Mutex
	at        time.Time
	windows   []Window
	signedOut bool
}

// agyUsageWindows is agy's /usage figures, and whether agy asked for
// authentication instead. The output itself is never kept: a sign-in prompt
// carries an OAuth URL with a state token.
func agyUsageWindows(now time.Time) ([]Window, bool) {
	agyUsage.Lock()
	defer agyUsage.Unlock()
	if !agyUsage.at.IsZero() && now.Sub(agyUsage.at) < agyOfferEvery {
		return slices.Clone(agyUsage.windows), agyUsage.signedOut
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	agyUsage.at = now
	out, err := agyUsageLister(ctx)
	agyUsage.windows, agyUsage.signedOut = nil, agyAsksToSignIn(out)
	if err != nil || agyUsage.signedOut {
		return nil, agyUsage.signedOut
	}
	agyUsage.windows = parseAgyUsage(out, now)
	return slices.Clone(agyUsage.windows), false
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
	agyUsage.Lock()
	agyUsage.at, agyUsage.windows, agyUsage.signedOut = time.Time{}, nil, false
	agyUsage.Unlock()
}
