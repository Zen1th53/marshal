package antigravity

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

// Model is one model the installed agy offers, as `agy models` lists it.
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Selectors name what a task needs instead of a model. Marshal resolves one
// against the models the signed-in agy offers right now, so a plan never
// has to name a model and a new model is used without a Marshal release:
//
//	auto       the Orchestrator (a planner model plans, Hydrogen executes)
//	           when offered, otherwise best
//	best, plan the strongest single model (newest generation, strongest
//	           family, high effort)
//	fast       the newest fast family (Hydrogen, then Flash) at high effort
//	carbon, argon, hydrogen, flash, orchestrator
//	           that family's newest model at high effort
//	carbon-low and similar
//	           that family at that effort
//
// An exact model ID passes through unchanged when agy offers it.
const (
	SelectAuto = "auto"
	SelectBest = "best"
	SelectPlan = "plan"
	SelectFast = "fast"
)

// familyStrength orders the Gemini families from strongest; an unknown
// family ranks below all of them until Marshal learns it.
var familyStrength = map[string]int{"carbon": 4, "argon": 3, "pro": 3, "hydrogen": 2, "flash": 1}

var effortRank = map[string]int{"low": 1, "medium": 2, "high": 3, "xhigh": 4}

var geminiModel = regexp.MustCompile(`^gemini-(\d+(?:\.\d+)?)-([a-z]+)(?:-(low|medium|high|xhigh))?$`)

type modelTraits struct {
	id           string
	generation   float64
	family       string
	effort       string
	orchestrator bool
	planner      string // the planning family of an Orchestrator
}

// strengthOf ranks a family. A Gemini family Marshal does not know yet is
// taken as the newest and so the strongest: new families appear under new
// names (Neon arrived as Carbon left), and holding a list would make Marshal
// prefer an older model until its next release.
func strengthOf(family string) int {
	if s, ok := familyStrength[family]; ok {
		return s
	}
	if family == "" || family == "orchestrator" {
		return 0
	}
	return 5
}

// plannerName is the planning family agy names in an Orchestrator's display
// name ("Orchestrator (Neon)"); the ID does not always carry it
// (gemini-pro-orchestrator-g1-pro is "Orchestrator (Argon)").
var plannerName = regexp.MustCompile(`(?i)orchestrator\s*\(([a-z0-9 ]+)\)`)

func traitsOf(id, name string) (modelTraits, bool) {
	t := modelTraits{id: id}
	lower := strings.ToLower(id)
	if strings.Contains(lower, "orchestrator") {
		t.orchestrator, t.family = true, "orchestrator"
		if m := plannerName.FindStringSubmatch(name); m != nil {
			t.planner = strings.ToLower(strings.TrimSpace(m[1]))
		} else if i := strings.LastIndex(lower, "orchestrator-"); i >= 0 {
			t.planner = lower[i+len("orchestrator-"):]
		}
		return t, true
	}
	m := geminiModel.FindStringSubmatch(lower)
	if m == nil {
		return t, false
	}
	t.generation, _ = strconv.ParseFloat(m[1], 64)
	t.family, t.effort = m[2], m[3]
	return t, true
}

// IsSelector reports whether s names a need or a family rather than a model.
func IsSelector(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", SelectAuto, SelectBest, SelectPlan, SelectFast, "orchestrator":
		return true
	}
	family, effort, hasEffort := strings.Cut(s, "-")
	// Any single family word is a selector (a family Marshal does not know
	// yet included); exact model IDs carry a generation and so a digit.
	if family == "" || strings.ContainsAny(family, "0123456789.") {
		return false
	}
	if !hasEffort {
		return true
	}
	_, ok := effortRank[effort]
	return ok
}

// Resolve turns a selector or model ID into one exact model agy offers.
func Resolve(selector string, models []Model) (string, error) {
	selector = strings.TrimSpace(selector)
	if len(models) == 0 {
		return "", fmt.Errorf("%w: agy offers no models; sign in to agy and try again", model.ErrUnavailable)
	}
	for _, m := range models {
		if m.ID == selector {
			return m.ID, nil
		}
	}
	key := strings.ToLower(selector)
	if !IsSelector(key) {
		return "", fmt.Errorf("%w: agy does not offer model %q; it offers %s (or use auto, best, fast, carbon, argon, hydrogen)", model.ErrInvalid, selector, modelIDs(models))
	}
	var gemini, orchestrators []modelTraits
	for _, m := range models {
		t, ok := traitsOf(m.ID, m.Name)
		switch {
		case !ok:
		case t.orchestrator:
			orchestrators = append(orchestrators, t)
		default:
			gemini = append(gemini, t)
		}
	}
	var pick string
	switch key {
	case "", SelectAuto:
		pick = strongestOrchestrator(orchestrators)
		if pick == "" {
			pick = strongest(gemini, nil, "high")
		}
	case SelectBest, SelectPlan:
		pick = strongest(gemini, nil, "high")
	case SelectFast:
		pick = strongest(gemini, func(t modelTraits) bool { return t.family == "hydrogen" }, "high")
		if pick == "" {
			pick = strongest(gemini, func(t modelTraits) bool { return t.family == "flash" }, "high")
		}
	case "orchestrator":
		pick = strongestOrchestrator(orchestrators)
	default:
		family, effort, _ := strings.Cut(key, "-")
		if effort == "" {
			effort = "high"
		}
		pick = strongest(gemini, func(t modelTraits) bool { return t.family == family }, effort)
	}
	if pick == "" {
		return "", fmt.Errorf("%w: agy offers no model for %q; it offers %s", model.ErrUnavailable, selector, modelIDs(models))
	}
	return pick, nil
}

// strongest is the newest generation of the strongest family, at the wanted
// effort when offered and otherwise the nearest effort below it (then above).
func strongest(candidates []modelTraits, keep func(modelTraits) bool, effort string) string {
	var pool []modelTraits
	for _, t := range candidates {
		if keep == nil || keep(t) {
			pool = append(pool, t)
		}
	}
	want := effortRank[effort]
	distance := func(t modelTraits) int {
		r, ok := effortRank[t.effort]
		if !ok {
			return 1 // an unsuffixed model is one step from any wanted effort
		}
		// A lower effort is the nearer substitute: it costs less and is
		// faster, which a higher one never is.
		if r <= want {
			return (want - r) * 2
		}
		return (r - want) * 5
	}
	sort.SliceStable(pool, func(i, j int) bool {
		a, b := pool[i], pool[j]
		if a.generation != b.generation {
			return a.generation > b.generation
		}
		if strengthOf(a.family) != strengthOf(b.family) {
			return strengthOf(a.family) > strengthOf(b.family)
		}
		if distance(a) != distance(b) {
			return distance(a) < distance(b)
		}
		return a.id < b.id
	})
	if len(pool) == 0 {
		return ""
	}
	return pool[0].id
}

func strongestOrchestrator(orchestrators []modelTraits) string {
	sort.SliceStable(orchestrators, func(i, j int) bool {
		a, b := strengthOf(orchestrators[i].planner), strengthOf(orchestrators[j].planner)
		if a != b {
			return a > b
		}
		return orchestrators[i].id < orchestrators[j].id
	})
	if len(orchestrators) == 0 {
		return ""
	}
	return orchestrators[0].id
}

func modelIDs(models []Model) string {
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return strings.Join(ids, ", ")
}

// ParseModels reads `agy models` output: one "<id>\t<name>" line per model.
func ParseModels(out string) []Model {
	var models []Model
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Fetching") {
			continue
		}
		id, name, _ := strings.Cut(line, "\t")
		id = strings.TrimSpace(id)
		if strings.ContainsAny(id, " /\\") || !agyModelID.MatchString(id) || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, Model{ID: id, Name: strings.TrimSpace(name)})
	}
	return models
}

var agyModelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// modelCacheTTL is how long a listing is reused: the offer changes with the
// account's plan or an agy update, never between two tasks of one run.
const modelCacheTTL = 10 * time.Minute

var modelCache struct {
	sync.Mutex
	binary string
	at     time.Time
	models []Model
}

// DiscoverModels lists the models the signed-in agy offers. It runs
// `agy models`, which reads the account's offer and changes nothing.
func DiscoverModels(ctx context.Context, binary string, runner adapter.ProcessRunner) ([]Model, error) {
	if binary == "" || runner == nil {
		return nil, fmt.Errorf("%w: agy binary and runner required", model.ErrInvalid)
	}
	modelCache.Lock()
	if modelCache.binary == binary && time.Since(modelCache.at) < modelCacheTTL && len(modelCache.models) > 0 {
		models := append([]Model(nil), modelCache.models...)
		modelCache.Unlock()
		return models, nil
	}
	modelCache.Unlock()
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := runner.Run(listCtx, adapter.Command{
		Path: binary, Args: []string{"models"},
		Env: append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1"),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: agy models: %v", model.ErrUnavailable, err)
	}
	if result.ExitCode != 0 || result.TimedOut || result.Cancelled {
		return nil, fmt.Errorf("%w: agy models did not finish (exit %d)", model.ErrUnavailable, result.ExitCode)
	}
	models := ParseModels(string(result.Stdout))
	if len(models) == 0 {
		return nil, fmt.Errorf("%w: agy listed no models; sign in to agy and try again", model.ErrUnavailable)
	}
	modelCache.Lock()
	modelCache.binary, modelCache.at, modelCache.models = binary, time.Now(), append([]Model(nil), models...)
	modelCache.Unlock()
	return models, nil
}

// ResolveInstalled resolves a selector against the installed agy's offer.
// When agy cannot be asked, auto resolves to "": no --model is passed and
// agy uses its own default, so an offline listing never stops work and no
// model name is fixed in Marshal.
func ResolveInstalled(ctx context.Context, binary string, runner adapter.ProcessRunner, selector string) (string, error) {
	if selector != "" && !IsSelector(selector) {
		return selector, nil // an exact ID is checked by agy itself
	}
	auto := strings.TrimSpace(selector) == "" || strings.EqualFold(strings.TrimSpace(selector), SelectAuto)
	models, err := DiscoverModels(ctx, binary, runner)
	if err == nil {
		var resolved string
		if resolved, err = Resolve(selector, models); err == nil {
			return resolved, nil
		}
	}
	if auto {
		return "", nil
	}
	return "", err
}

// resetModelCache forgets the cached listing; tests use it.
func resetModelCache() {
	modelCache.Lock()
	modelCache.binary, modelCache.at, modelCache.models = "", time.Time{}, nil
	modelCache.Unlock()
}
