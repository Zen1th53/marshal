package bench

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/optimization"
)

// SupportedHarness names a single-agent harness the pack expects a baseline
// comparison to cover.
type SupportedHarness string

const (
	HarnessCodex       SupportedHarness = "codex"
	HarnessClaudeCode  SupportedHarness = "claude"
	HarnessOpenCode    SupportedHarness = "opencode"
	HarnessAntigravity SupportedHarness = "antigravity"
	HarnessGemini      SupportedHarness = "gemini"
	HarnessAider       SupportedHarness = "aider"
	HarnessCrush       SupportedHarness = "crush"
)

// implementedHarnesses lists the harnesses this repository has an adapter
// for today (internal/adapter/{codex,claude,opencode,antigravity,gemini}).
// Aider and Crush are named by the pack as "other supported harnesses where
// practical" but have no adapter here, so a baseline naming them is honest
// only when it also says NOT_RUN and why.
var implementedHarnesses = map[SupportedHarness]bool{
	HarnessCodex:       true,
	HarnessClaudeCode:  true,
	HarnessOpenCode:    true,
	HarnessAntigravity: true,
	HarnessGemini:      true,
}

// HasAdapter reports whether this repository can actually drive h, as
// opposed to merely knowing its name.
func HasAdapter(h SupportedHarness) bool {
	return implementedHarnesses[h]
}

// CapabilityDifference records one place a harness's native behavior differs
// from the others in a baseline comparison.
//
// The pack requires these be documented, not smoothed over. A comparison that
// silently drops a harness's native subagents or its own retry policy so all
// harnesses "look the same" is not a fair comparison; it is a comparison of a
// fiction none of the harnesses actually run as.
type CapabilityDifference struct {
	Harness SupportedHarness `json:"harness"`
	// Dimension names what differs: "tool_access", "context_window",
	// "native_subagents", "retry_policy", "sandboxing", etc.
	Dimension string `json:"dimension"`
	// Detail is the concrete difference, stated so a reader can judge whether
	// it affects the comparison's fairness.
	Detail string `json:"detail"`
	// Flattened would be true only if the harness's actual behavior were
	// overridden to match another harness's default. This package never sets
	// it true itself; a caller who deliberately equalizes something is
	// expected to record that decision here rather than let it vanish.
	Flattened bool `json:"flattened"`
}

// TaskBudget bounds what a baseline task run may spend across every harness
// under comparison.
type TaskBudget struct {
	BudgetMicros  int64
	WallMillis    int64
	MaxModelCalls int64
}

// BaselineConfig is one harness's slot in a single-agent baseline comparison.
type BaselineConfig struct {
	Harness        SupportedHarness
	HarnessVersion string
	Model          string
	ModelVersion   string
	// ToolAccess lists what tools the harness was permitted for this run. It
	// is part of the record because two harnesses given different tool
	// access are not comparable even on the same task set.
	ToolAccess []string
	Budget     TaskBudget
	// CapabilityDifferences documents this harness's native behavior that
	// could not be equalized with the others, and why that is acceptable.
	CapabilityDifferences []CapabilityDifference
}

// BaselineSuite is a single-agent baseline comparison across harnesses on one
// shared task set.
type BaselineSuite struct {
	ID              string
	DatasetSnapshot string
	TaskIDs         []string
	EnvironmentImage string
	MarshalSHA       string
	Configs          []BaselineConfig
}

// ValidateBaselineSuite refuses a baseline suite that could not authorize a fair
// comparison.
//
// "Fair" here means the same task set, a bounded budget and declared tool
// access for every harness in the suite — not identical native behavior,
// which ValidateBaselineSuite does not require and must not require.
func ValidateBaselineSuite(s BaselineSuite) error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("%w: baseline suite id", optimization.ErrInvalid)
	}
	if strings.TrimSpace(s.DatasetSnapshot) == "" || len(s.TaskIDs) == 0 {
		return fmt.Errorf("%w: baseline suite names no dataset snapshot or task set", optimization.ErrInvalid)
	}
	if strings.TrimSpace(s.EnvironmentImage) == "" || strings.TrimSpace(s.MarshalSHA) == "" {
		return fmt.Errorf("%w: baseline suite is not bound to an exact environment and MARSHAL state", optimization.ErrInvalid)
	}
	if len(s.Configs) < 2 {
		return fmt.Errorf("%w: baseline suite compares fewer than two harnesses", optimization.ErrInvalid)
	}
	seen := map[SupportedHarness]bool{}
	for _, c := range s.Configs {
		if strings.TrimSpace(string(c.Harness)) == "" || strings.TrimSpace(c.HarnessVersion) == "" {
			return fmt.Errorf("%w: baseline config names no harness or harness version", optimization.ErrInvalid)
		}
		if seen[c.Harness] {
			return fmt.Errorf("%w: harness %s appears twice in one baseline suite", optimization.ErrInvalid, c.Harness)
		}
		seen[c.Harness] = true
		if strings.TrimSpace(c.Model) == "" || strings.TrimSpace(c.ModelVersion) == "" {
			return fmt.Errorf("%w: baseline config for %s does not pin a model", optimization.ErrInvalid, c.Harness)
		}
		if c.Budget.BudgetMicros <= 0 || c.Budget.WallMillis <= 0 {
			return fmt.Errorf("%w: baseline config for %s has no bounded budget", optimization.ErrInvalid, c.Harness)
		}
		if len(c.ToolAccess) == 0 {
			return fmt.Errorf("%w: baseline config for %s declares no tool access", optimization.ErrInvalid, c.Harness)
		}
	}
	return nil
}

// EqualBudgets reports whether every config in the suite shares the same
// budget bounds. Comparing unequal budgets is not forbidden by itself — the
// pack forbids doing so without disclosure — but a caller needs this to
// decide whether disclosure is required.
func EqualBudgets(s BaselineSuite) bool {
	if len(s.Configs) == 0 {
		return true
	}
	want := s.Configs[0].Budget
	for _, c := range s.Configs[1:] {
		if c.Budget != want {
			return false
		}
	}
	return true
}

// BaselineOutcome is one harness's result on one task within a baseline
// comparison.
type BaselineOutcome struct {
	Harness       SupportedHarness
	TaskID        string
	Outcome       optimization.Status
	Metrics       []optimization.Metric
	FailureReason string
	ObservedAt    time.Time
}

// ProbeHarness reports whether this repository can actually drive h for a
// baseline run, distinct from whether h is merely on the pack's supported
// list.
//
// Naming a harness in a comparison and being able to execute it are different
// claims. A baseline suite that includes Aider or Crush by name but has no
// adapter for either must report NOT_RUN for those rows, not silently drop
// the harness from the table.
func ProbeHarness(h SupportedHarness) Probe {
	if HasAdapter(h) {
		return Probe{Available: true}
	}
	return Probe{
		Available: false,
		Reason:    fmt.Sprintf("no MARSHAL adapter implements harness %q in this repository", h),
	}
}

// RunBaseline evaluates one config against one task, deferring to results
// already produced elsewhere (adapter runs are out of this package's scope);
// it exists to turn a probed-unavailable harness into an honest NOT_RUN row
// rather than silently omitting it from the suite.
func RunBaseline(cfg BaselineConfig, taskID string, now time.Time, produced *BaselineOutcome) BaselineOutcome {
	probe := ProbeHarness(cfg.Harness)
	if !probe.Available {
		return BaselineOutcome{
			Harness:       cfg.Harness,
			TaskID:        taskID,
			Outcome:       optimization.StatusNotRun,
			FailureReason: probe.Reason,
			ObservedAt:    now,
		}
	}
	if produced != nil {
		return *produced
	}
	// The harness has an adapter but no result was supplied: this package
	// does not itself drive internal/adapter sessions (that belongs to the
	// execution layer, not the bench harness), so the honest answer is still
	// NOT_RUN rather than a fabricated pass.
	return BaselineOutcome{
		Harness:       cfg.Harness,
		TaskID:        taskID,
		Outcome:       optimization.StatusNotRun,
		FailureReason: "harness adapter available but no execution result was supplied to the bench layer",
		ObservedAt:    now,
	}
}

// CapabilityReport summarizes the documented capability differences across a
// baseline suite, grouped by harness so a reader sees each harness's native
// profile rather than a flattened union.
type CapabilityReport struct {
	ByHarness map[SupportedHarness][]CapabilityDifference
}

// BuildCapabilityReport preserves each harness's declared differences without
// merging or discarding any. A difference marked Flattened is kept, not
// dropped: hiding that a difference was flattened would itself understate how
// comparable the suite really is.
func BuildCapabilityReport(s BaselineSuite) CapabilityReport {
	report := CapabilityReport{ByHarness: map[SupportedHarness][]CapabilityDifference{}}
	for _, c := range s.Configs {
		diffs := append([]CapabilityDifference(nil), c.CapabilityDifferences...)
		sort.Slice(diffs, func(i, j int) bool {
			if diffs[i].Dimension != diffs[j].Dimension {
				return diffs[i].Dimension < diffs[j].Dimension
			}
			return diffs[i].Detail < diffs[j].Detail
		})
		report.ByHarness[c.Harness] = diffs
	}
	return report
}
