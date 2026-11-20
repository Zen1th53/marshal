package antigravity

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

// ultraListing is `agy models` from agy 1.5.10 signed in with Google AI Ultra.
const ultraListing = "Fetching available models...\n" +
	"gemini-4-carbon-medium\tGemini 4 Carbon (Medium)\n" +
	"gemini-4-carbon-high\tGemini 4 Carbon (High)\n" +
	"gemini-4-carbon-low\tGemini 4 Carbon (Low)\n" +
	"gemini-4-argon-medium\tGemini 4 Argon (Medium)\n" +
	"gemini-4-argon-high\tGemini 4 Argon (High)\n" +
	"gemini-4-argon-low\tGemini 4 Argon (Low)\n" +
	"gemini-4-hydrogen-high\tGemini 4 Hydrogen (High)\n" +
	"gemini-4-hydrogen-xhigh\tGemini 4 Hydrogen (XHigh)\n" +
	"gemini-4-hydrogen-medium\tGemini 4 Hydrogen (Medium)\n" +
	"gemini-4-hydrogen-low\tGemini 4 Hydrogen (Low)\n" +
	"gemini-pro-orchestrator\tOrchestrator (Carbon)\n" +
	"gemini-pro-orchestrator-argon\tOrchestrator (Argon)\n" +
	"gemini-3.8-flash-high\tGemini 3.8 Flash (High)\n" +
	"gemini-3.8-flash-medium\tGemini 3.8 Flash (Medium)\n" +
	"gemini-3.8-flash-low\tGemini 3.8 Flash (Low)\n" +
	"claude-sonnet-4-6\tClaude Sonnet 4.6 (Thinking)\n" +
	"claude-opus-4-6-thinking\tClaude Opus 4.6 (Thinking)\n" +
	"gpt-oss-120b-medium\tGPT-OSS 120B (Medium)\n"

func TestParseModelsReadsTheListing(t *testing.T) {
	models := ParseModels(ultraListing + "bad id with spaces\tx\n../escape\tx\ngemini-4-carbon-high\tduplicate\n")
	if len(models) != 18 {
		t.Fatalf("models = %d: %v", len(models), models)
	}
	if models[10] != (Model{ID: "gemini-pro-orchestrator", Name: "Orchestrator (Carbon)"}) {
		t.Fatalf("orchestrator = %+v", models[10])
	}
}

func TestSelectorsResolveAgainstTheOffer(t *testing.T) {
	ultra := ParseModels(ultraListing)
	pro := ParseModels("gemini-4-argon-high\tA\ngemini-4-argon-low\tA\ngemini-4-hydrogen-high\tH\ngemini-pro-orchestrator-argon\tO\ngemini-3.8-flash-high\tF\n")
	apiKey := ParseModels("gemini-4-hydrogen-low\tH\ngemini-4-hydrogen-xhigh\tH\ngemini-4-carbon-medium\tC\ngemini-3.8-flash-high\tF\n")
	flashOnly := ParseModels("gemini-3.8-flash-high\tF\ngemini-3.8-flash-low\tF\n")
	future := ParseModels("gemini-5-carbon-high\tC5\ngemini-4-carbon-high\tC4\ngemini-5-hydrogen-high\tH5\n")
	for _, tc := range []struct {
		name, selector string
		models         []Model
		want           string
	}{
		{"ultra auto", "auto", ultra, "gemini-pro-orchestrator"},
		{"ultra empty", "", ultra, "gemini-pro-orchestrator"},
		{"ultra best", "best", ultra, "gemini-4-carbon-high"},
		{"ultra plan", "plan", ultra, "gemini-4-carbon-high"},
		{"ultra fast", "fast", ultra, "gemini-4-hydrogen-high"},
		{"ultra argon", "argon", ultra, "gemini-4-argon-high"},
		{"ultra hydrogen xhigh", "hydrogen-xhigh", ultra, "gemini-4-hydrogen-xhigh"},
		{"ultra carbon low", "Carbon-Low", ultra, "gemini-4-carbon-low"},
		{"ultra flash", "flash", ultra, "gemini-3.8-flash-high"},
		{"ultra exact", "claude-opus-4-6-thinking", ultra, "claude-opus-4-6-thinking"},
		{"pro auto takes the argon orchestrator", "auto", pro, "gemini-pro-orchestrator-argon"},
		{"pro best", "best", pro, "gemini-4-argon-high"},
		{"api key auto without orchestrator", "auto", apiKey, "gemini-4-carbon-medium"},
		{"api key fast nearest effort", "fast", apiKey, "gemini-4-hydrogen-low"},
		{"flash only auto", "auto", flashOnly, "gemini-3.8-flash-high"},
		{"flash only fast", "fast", flashOnly, "gemini-3.8-flash-high"},
		{"a newer generation wins without a release", "best", future, "gemini-5-carbon-high"},
		{"a newer fast family wins", "fast", future, "gemini-5-hydrogen-high"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.selector, tc.models)
			if err != nil || got != tc.want {
				t.Fatalf("Resolve(%q) = %q, %v; want %q", tc.selector, got, err, tc.want)
			}
		})
	}
}

func TestResolveRefusesWhatIsNotOffered(t *testing.T) {
	ultra := ParseModels(ultraListing)
	if _, err := Resolve("gemini-9-imaginary", ultra); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("unknown exact model = %v", err)
	}
	flashOnly := ParseModels("gemini-3.8-flash-high\tF\n")
	if _, err := Resolve("carbon", flashOnly); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("carbon on a flash-only account = %v", err)
	}
	if _, err := Resolve("orchestrator", flashOnly); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("orchestrator on a flash-only account = %v", err)
	}
}

type listingRunner struct {
	out   string
	calls int
	fail  bool
}

func (r *listingRunner) Run(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
	r.calls++
	if r.fail {
		return adapter.ProcessResult{ExitCode: 1}, nil
	}
	return adapter.ProcessResult{Stdout: []byte(r.out)}, nil
}

func TestResolveInstalledAsksAgyOnceAndFallsBack(t *testing.T) {
	resetModelCache()
	runner := &listingRunner{out: ultraListing}
	for i := 0; i < 3; i++ {
		got, err := ResolveInstalled(t.Context(), "/fake/agy", runner, "auto")
		if err != nil || got != "gemini-pro-orchestrator" {
			t.Fatalf("auto = %q, %v", got, err)
		}
	}
	if runner.calls != 1 {
		t.Fatalf("agy models ran %d times; the listing is reused", runner.calls)
	}
	if got, _ := ResolveInstalled(t.Context(), "/fake/agy", runner, "gemini-4-argon-low"); got != "gemini-4-argon-low" {
		t.Fatalf("exact = %q", got)
	}

	resetModelCache()
	broken := &listingRunner{fail: true}
	if got, err := ResolveInstalled(t.Context(), "/fake/agy", broken, ""); err != nil || got != "" {
		t.Fatalf("auto without a listing = %q, %v; want agy's own default (no model)", got, err)
	}
	if _, err := ResolveInstalled(t.Context(), "/fake/agy", broken, "carbon"); err == nil {
		t.Fatal("an explicit family without a listing resolved silently")
	}
}

func TestIsSelector(t *testing.T) {
	for s, want := range map[string]bool{
		"": true, "auto": true, "BEST": true, "fast": true, "plan": true, "orchestrator": true,
		"carbon": true, "argon-low": true, "hydrogen-xhigh": true, "flash": true,
		"gemini-4-carbon-high": false, "carbon-huge": false, "claude-opus-4-6-thinking": false,
	} {
		if IsSelector(s) != want {
			t.Errorf("IsSelector(%q) = %v", s, !want)
		}
	}
}

// The offer of 19 November: Carbon gone, a Neon family and two
// Orchestrators whose planners only their names say. A family Marshal does
// not know is taken as the newest, so best is Neon, not the older Argon.
const ultraListingNeon = "Fetching available models...\n" +
	"gemini-4-neon-high\tGemini 4 Neon (High)\n" +
	"gemini-4-neon-xhigh\tGemini 4 Neon (XHigh)\n" +
	"gemini-4-neon-medium\tGemini 4 Neon (Medium)\n" +
	"gemini-4-neon-low\tGemini 4 Neon (Low)\n" +
	"gemini-4-argon-medium\tGemini 4 Argon (Medium)\n" +
	"gemini-4-argon-high\tGemini 4 Argon (High)\n" +
	"gemini-4-hydrogen-high\tGemini 4 Hydrogen (High)\n" +
	"gemini-4-hydrogen-low\tGemini 4 Hydrogen (Low)\n" +
	"gemini-pro-orchestrator-neon\tOrchestrator (Neon)\n" +
	"gemini-pro-orchestrator-g1-pro\tOrchestrator (Argon)\n" +
	"gemini-3.8-flash-high\tGemini 3.8 Flash (High)\n" +
	"claude-opus-5-5-high\tClaude Opus 5.5 (High)\n" +
	"gpt-oss-120b-medium\tGPT-OSS 120B (Medium)\n"

func TestSelectorsFollowNewFamilies(t *testing.T) {
	models := ParseModels(ultraListingNeon)
	for selector, want := range map[string]string{
		"auto": "gemini-pro-orchestrator-neon", "orchestrator": "gemini-pro-orchestrator-neon",
		"best": "gemini-4-neon-high", "neon": "gemini-4-neon-high", "neon-xhigh": "gemini-4-neon-xhigh",
		"argon": "gemini-4-argon-high", "fast": "gemini-4-hydrogen-high", "flash": "gemini-3.8-flash-high",
	} {
		if got, err := Resolve(selector, models); err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", selector, got, err, want)
		}
	}
	if !IsSelector("neon") || IsSelector("gemini-4-neon-high") {
		t.Fatal("a new family word is not a selector, or an exact id became one")
	}
}
