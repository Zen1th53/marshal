package usage

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `agy -p /usage` as the workstation printed it on 21 November (NO_COLOR=1
// TERM=dumb CI=1).
const agyWorkstationUsage = "Gemini Models\tWeekly Limit Remaining\t96%\t2026-11-28T12:52:41Z\n" +
	"Gemini Models\tFive Hour Limit Remaining\t88%\t2026-11-21T17:52:41Z\n" +
	"Claude and GPT models\tWeekly Limit Remaining\t0%\t2026-11-26T04:32:28Z\n" +
	"Claude and GPT models\tFive Hour Limit Remaining\t100%\t2026-11-21T21:04:27Z\n" +
	"Gemini 4 Argon\tGemini 4 Argon Weekly Limit\t97%\t2026-11-28T09:15:56Z\n"

// Every model `agy models` lists falls in the group it runs on: Argon
// models on the Gemini 4 Argon group, every other Gemini model on Gemini,
// Claude and GPT-OSS on Claude and GPT (spent).
func TestAgyWorkstationGroups(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-21T16:37:00Z")
	q := Quota{Provider: "agy", Known: true, AsOf: now, Windows: parseAgyUsage([]byte(agyWorkstationUsage), now)}
	if len(q.Windows) != 5 {
		t.Fatalf("windows = %+v", q.Windows)
	}
	for model, want := range map[string]struct {
		labels string
		left   int
	}{
		"gemini-4-argon-high":          {"Gemini 4 Argon wk", 97},
		"gemini-4-neon-high":           {"Gemini wk,Gemini 5h", 88},
		"gemini-4-hydrogen-high":       {"Gemini wk,Gemini 5h", 88},
		"gemini-pro-orchestrator-high": {"Gemini wk,Gemini 5h", 88},
		"gemini-3.8-flash-high":        {"Gemini wk,Gemini 5h", 88},
		"claude-opus-5-5-high":         {"Claude and GPT wk,Claude and GPT 5h", 0},
		"claude-sonnet-5-5-high":       {"Claude and GPT wk,Claude and GPT 5h", 0},
		"gpt-oss-120b-medium":          {"Claude and GPT wk,Claude and GPT 5h", 0},
	} {
		wins, ok := q.ModelWindows(model)
		var labels []string
		for _, w := range wins {
			labels = append(labels, w.Label)
		}
		if !ok || strings.Join(labels, ",") != want.labels {
			t.Errorf("%s windows = %v %v; want %s", model, labels, ok, want.labels)
		}
		if w, ok := q.ModelBinding(model); !ok || 100-w.UsedShown() != want.left {
			t.Errorf("%s binding = %+v %v; want %d%% left", model, w, ok, want.left)
		}
	}
	// Without its own group an Argon model runs on Gemini.
	noArgon := Quota{Provider: "agy", Known: true, Windows: q.Windows[:4]}
	if w, ok := noArgon.ModelBinding("gemini-4-argon-high"); !ok || w.Label != "Gemini 5h" {
		t.Fatalf("argon without its group = %+v %v", w, ok)
	}
}

// A signed-out agy prints a sign-in URL instead of figures and waits until
// the bound. agy is not signed in, never at its limit, and the URL with its
// state token is kept nowhere.
func TestAgyAsksForAuthentication(t *testing.T) {
	t.Setenv("MARSHAL_NO_USAGE_PROBES", "")
	saved, savedUsage := agyModelsLister, agyUsageLister
	t.Cleanup(func() { agyModelsLister, agyUsageLister = saved, savedUsage; resetAgyOffer() })
	resetAgyOffer()
	agyModelsLister = agyListing("gemini-4-argon-high", "claude-opus-5-5-high")
	const prompt = "Authentication required. Please visit the URL https://accounts.example.com/o/oauth2/auth?client_id=x&state=STATE-TOKEN-1234 to sign in.\n"
	agyUsageLister = func(context.Context) ([]byte, error) {
		return []byte(prompt), context.DeadlineExceeded
	}
	now, _ := time.Parse(time.RFC3339, "2026-11-21T16:37:00Z")
	q := readAgy(now, filepath.Join(t.TempDir(), "seen.json"))
	if q.Reason != ReasonNotSignedIn || q.Known || len(q.Windows) != 0 || q.Source() != "not signed in" {
		t.Fatalf("signed-out agy = %+v", q)
	}
	if s := fmt.Sprintf("%#v", q); strings.Contains(s, "STATE-TOKEN") || strings.Contains(s, "https://") {
		t.Fatalf("the sign-in URL is kept: %s", s)
	}
	// The cached answer says the same, still without the URL.
	if q2 := readAgy(now.Add(time.Minute), ""); q2.Reason != ReasonNotSignedIn {
		t.Fatalf("cached = %+v", q2)
	}
}
