package usage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A rate_limit_event exactly as Claude Code 2.1.326 prints it in stream-json.
const claudeEvent = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1795009200,"rateLimitType":"five_hour","overageStatus":"rejected","isUsingOverage":false,"unifiedWindows":{"five_hour":{"utilization":0.13,"resetsAt":1795009200,"status":"allowed"},"seven_day":{"utilization":0.28,"resetsAt":1795370400,"status":"allowed"}}},"session_id":"s"}`

// Claude no longer writes cachedUsageUtilization; its limits come from the
// last rate_limit_event MARSHAL saw.
func TestClaudeLimitsFromRateLimitEvent(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "claude-rate-limits.json")
	seen := time.Unix(1794990000, 0)
	recordClaudeRateLimitAt([]byte(`{"type":"assistant"}`), seen, cache)
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("a line that is not a rate_limit_event was recorded")
	}
	recordClaudeRateLimitAt([]byte(claudeEvent), seen, cache)

	claudeJSON := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(claudeJSON, []byte(`{"cachedExtraUsageDisabledReason":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	now := seen.Add(10 * time.Minute)
	q := readClaudeWithEvents(now, claudeJSON, cache)
	if !q.Known || q.Stale || len(q.Windows) != 2 {
		t.Fatalf("quota = %+v", q)
	}
	five := q.Windows[0]
	if five.Label != "5h" || five.UsedShown() != 13 || !five.ResetsAt.Equal(time.Unix(1795009200, 0)) {
		t.Fatalf("5h window = %+v", five)
	}
	if wk := q.Windows[1]; wk.Label != "wk" || wk.UsedShown() != 28 {
		t.Fatalf("week window = %+v", wk)
	}
	if w, _ := q.Binding(); w.Label != "wk" {
		t.Fatalf("binding window = %+v, want the fuller week", w)
	}
	// After the 5h window resets its figure no longer holds; an hour old
	// record is shown as old.
	late := time.Unix(1795009200, 0).Add(time.Minute)
	q = readClaudeWithEvents(late, claudeJSON, cache)
	if q.Windows[0].Known() || !q.Windows[1].Known() || !q.Stale {
		t.Fatalf("after reset: %+v", q)
	}
}

func agyListing(lines ...string) func(context.Context) ([]byte, error) {
	out := "Fetching available models...\n"
	for _, l := range lines {
		out += l + "\tName\n"
	}
	return func(context.Context) ([]byte, error) { return []byte(out), nil }
}

// agy reports no figures but drops a family while its quota is spent: the
// Ultra account offered Gemini 4 and the Orchestrators, then only Flash,
// Claude and GPT-OSS.
func TestAgyOfferShowsSpentFamilies(t *testing.T) {
	t.Setenv("MARSHAL_NO_USAGE_PROBES", "")
	saved, savedUsage := agyModelsLister, agyUsageLister
	t.Cleanup(func() { agyModelsLister, agyUsageLister = saved, savedUsage; resetAgyOffer() })
	agyUsageLister = func(context.Context) ([]byte, error) { return nil, errors.New("no usage") }
	seen := filepath.Join(t.TempDir(), "agy-model-groups.json")
	now := time.Unix(1795000000, 0)

	resetAgyOffer()
	agyModelsLister = agyListing("gemini-4-carbon-high", "gemini-4-hydrogen-low", "gemini-pro-orchestrator", "gemini-pro-orchestrator-argon", "gemini-3.8-flash-high", "claude-opus-4-6-thinking", "gpt-oss-120b-medium")
	q := readAgy(now, seen)
	if len(q.Offer) != 5 || q.OfferedNone() {
		t.Fatalf("full offer = %+v", q.Offer)
	}

	resetAgyOffer()
	agyModelsLister = agyListing("gemini-3.8-flash-high", "gemini-3.8-flash-low", "claude-sonnet-4-6", "gpt-oss-120b-medium")
	q = readAgy(now.Add(time.Hour), seen)
	state := map[string]bool{}
	for _, g := range q.Offer {
		state[g.Name] = g.Offered
	}
	want := map[string]bool{"Gemini 4": false, "Orchestrator": false, "Flash": true, "Claude": true, "GPT-OSS": true}
	for name, offered := range want {
		if got, ok := state[name]; !ok || got != offered {
			t.Fatalf("%s offered = %v (present %v), want %v; offer %+v", name, got, ok, offered, q.Offer)
		}
	}

	resetAgyOffer()
	agyModelsLister = agyListing()
	if q = readAgy(now.Add(2*time.Hour), seen); !q.OfferedNone() {
		t.Fatalf("an empty listing left agy usable: %+v", q.Offer)
	}

	resetAgyOffer()
	agyModelsLister = func(context.Context) ([]byte, error) { return nil, errors.New("not installed") }
	if q = readAgy(now.Add(3*time.Hour), seen); q.Reason != NotExposed || len(q.Offer) != 0 {
		t.Fatalf("an unreadable listing = %+v", q)
	}
}

// Read only runs agy when asked, so tests and callers without it never exec it.
func TestReadRunsAgyOnlyWhenAsked(t *testing.T) {
	saved := agyModelsLister
	t.Cleanup(func() { agyModelsLister = saved; resetAgyOffer() })
	resetAgyOffer()
	called := false
	agyModelsLister = func(context.Context) ([]byte, error) { called = true; return nil, nil }
	if qs := Read(time.Now(), Sources{}); qs[3].Reason != NotExposed || called {
		t.Fatalf("agy read without being asked: %+v %v", qs[3], called)
	}
}

// `agy -p /usage` as agy 1.5.10 printed it on 19 November.
const agyUsageOut = "Gemini Models\tWeekly Limit Remaining\t100%\t2026-11-26T03:51:20Z\n" +
	"Gemini Models\tFive Hour Limit Remaining\t100%\t2026-11-19T08:51:20Z\n" +
	"Claude and GPT models\tWeekly Limit Remaining\t100%\t2026-11-26T03:51:20Z\n" +
	"Claude and GPT models\tFive Hour Limit Remaining\t100%\t2026-11-19T08:51:20Z\n" +
	"Gemini 4 Argon\tGemini 4 Argon Weekly Limit\t96%\t2026-11-24T23:48:16Z\n"

func TestAgyUsageFigures(t *testing.T) {
	t.Setenv("MARSHAL_NO_USAGE_PROBES", "")
	saved, savedUsage := agyModelsLister, agyUsageLister
	t.Cleanup(func() { agyModelsLister, agyUsageLister = saved, savedUsage; resetAgyOffer() })
	resetAgyOffer()
	agyModelsLister = agyListing("gemini-4-argon-high", "claude-opus-5-5-high")
	agyUsageLister = func(context.Context) ([]byte, error) { return []byte(agyUsageOut), nil }
	now, _ := time.Parse(time.RFC3339, "2026-11-19T04:00:00Z")
	q := readAgy(now, filepath.Join(t.TempDir(), "seen.json"))
	if !q.Known || len(q.Windows) != 5 {
		t.Fatalf("quota = %+v", q)
	}
	w, ok := q.Binding()
	if !ok || w.Label != "Gemini 4 Argon wk" || w.UsedShown() != 4 {
		t.Fatalf("binding = %+v %v; want the Argon weekly window at 96%% left", w, ok)
	}
	labels := map[string]bool{}
	for _, w := range q.Windows {
		labels[w.Label] = true
	}
	for _, l := range []string{"Gemini wk", "Gemini 5h", "Claude and GPT wk", "Claude and GPT 5h"} {
		if !labels[l] {
			t.Errorf("missing window %s: %v", l, labels)
		}
	}
}
