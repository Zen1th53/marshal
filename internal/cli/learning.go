package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/model"
)

const learningUsage = "Usage: marshal learning commit INPUT.json | show MEMORY-COMMIT-ID | item ITEM-ID | history ITEM-ID | search --project ID [--general] [--terms A,B] [--stale] | context --project ID [--general] [--constraint TEXT] | invalidate INPUT.json | trust [TASK-CLASS] | fingerprints --project ID | playbooks --project ID | replays [RUN-ID] | benchmarks [BENCHMARK] | export --project ID [--general] | restore BUNDLE.json --project ID [--general]"

func (c command) learning(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: %s", model.ErrInvalid, learningUsage)
	}
	switch args[0] {
	case "commit", "show", "item", "history", "search", "context", "invalidate",
		"trust", "fingerprints", "playbooks", "replays", "benchmarks", "export", "restore":
	default:
		return fmt.Errorf("%w: %s", model.ErrInvalid, learningUsage)
	}
	runtime, err := app.Open(ctx, c.root)
	if err != nil {
		return err
	}
	defer runtime.Close()
	service := runtime.Learning()

	switch args[0] {
	case "commit":
		return c.learningCommit(ctx, service, args[1:])
	case "show":
		if len(args) != 2 {
			return fmt.Errorf("%w: memory commit ID required", model.ErrInvalid)
		}
		got, err := service.Get(ctx, args[1])
		if err != nil {
			return err
		}
		return c.print(got, fmt.Sprintf("memory-commit=%s outcome=%s additions=%d revisions=%d blocked=%d",
			got.ID, got.Binding.Outcome, len(got.Additions), len(got.Revisions), len(got.BlockedLearning)))
	case "item":
		if len(args) != 2 {
			return fmt.Errorf("%w: item ID required", model.ErrInvalid)
		}
		got, err := service.Item(ctx, args[1])
		if err != nil {
			return err
		}
		return c.print(got, fmt.Sprintf("item=%s state=%s scope=%s version=%d", got.ID, got.State, got.Scope, got.Version))
	case "history":
		if len(args) != 2 {
			return fmt.Errorf("%w: item ID required", model.ErrInvalid)
		}
		got, err := service.History(ctx, args[1])
		if err != nil {
			return err
		}
		lines := make([]string, 0, len(got))
		for _, it := range got {
			lines = append(lines, fmt.Sprintf("v%d %s", it.Version, it.State))
		}
		return c.print(got, strings.Join(lines, "\n"))
	case "search":
		return c.learningSearch(ctx, service, args[1:])
	case "context":
		return c.learningContext(ctx, service, args[1:])
	case "invalidate":
		return c.learningInvalidate(ctx, service, args[1:])
	case "trust":
		taskClass := ""
		if len(args) == 2 {
			taskClass = args[1]
		}
		got, err := service.Trust(ctx, taskClass)
		if err != nil {
			return err
		}
		lines := make([]string, 0, len(got))
		for key, trust := range got {
			// Cost and latency print as "unmeasured" when nothing was measured;
			// they are never rendered as zero.
			lines = append(lines, fmt.Sprintf("%s %s/%s verified=%d partial=%d failed=%d blocked=%d selection_bias=%t cost=%s latency=%s",
				key.TaskClass, key.Provider, key.ProviderVersion,
				trust.Verified, trust.Partial, trust.Failed, trust.Blocked,
				trust.SelectionBiased, optionalInt(trust.MeasuredCostMicros), optionalInt(trust.MeasuredLatencyMillis)))
		}
		return c.print(got, strings.Join(lines, "\n"))
	case "fingerprints":
		project, err := requiredFlag(args[1:], "--project")
		if err != nil {
			return err
		}
		got, err := service.Fingerprints(ctx, project)
		if err != nil {
			return err
		}
		lines := make([]string, 0, len(got))
		for _, f := range got {
			lines = append(lines, fmt.Sprintf("%s %s occurrences=%d scope=%s", f.ID, f.Signature, f.Occurrences, f.Scope))
		}
		return c.print(got, strings.Join(lines, "\n"))
	case "playbooks":
		project, err := requiredFlag(args[1:], "--project")
		if err != nil {
			return err
		}
		got, err := service.Playbooks(ctx, project)
		if err != nil {
			return err
		}
		lines := make([]string, 0, len(got))
		for _, p := range got {
			// A candidate is never active. Saying so on every line keeps the
			// surface honest about what review still owes.
			lines = append(lines, fmt.Sprintf("%s %q scope=%s active=false (awaiting review)", p.ID, p.Title, p.Scope))
		}
		return c.print(got, strings.Join(lines, "\n"))
	case "replays":
		runID := ""
		if len(args) == 2 {
			runID = args[1]
		}
		got, err := service.Replays(ctx, runID)
		if err != nil {
			return err
		}
		lines := make([]string, 0, len(got))
		for _, r := range got {
			lines = append(lines, fmt.Sprintf("%s class=%s automatic=%t", r.ID, r.Class, r.Replayable()))
		}
		return c.print(got, strings.Join(lines, "\n"))
	case "benchmarks":
		benchmark := ""
		if len(args) == 2 {
			benchmark = args[1]
		}
		got, err := service.Benchmarks(ctx, benchmark)
		if err != nil {
			return err
		}
		lines := make([]string, 0, len(got))
		for _, b := range got {
			lines = append(lines, fmt.Sprintf("%s %s@%s task=%s resolved=%s verifier=%s", b.ID, b.Benchmark, b.Version, b.TaskID, b.Resolved, b.VerifierResult))
		}
		return c.print(got, strings.Join(lines, "\n"))
	case "export":
		project, err := requiredFlag(args[1:], "--project")
		if err != nil {
			return err
		}
		got, err := service.Export(ctx, project, hasFlag(args[1:], "--general"))
		if err != nil {
			return err
		}
		return c.print(got, fmt.Sprintf("export schema=%d items=%d redacted=%d digest=%s",
			got.SchemaVersion, len(got.Items), len(got.Redacted), got.Digest))
	case "restore":
		return c.learningRestore(ctx, service, args[1:])
	}
	return nil
}

// optionalInt renders an unmeasured metric honestly. A nil pointer means the
// value was never measured, which is not the same as zero.
func optionalInt(v *int64) string {
	if v == nil {
		return "unmeasured"
	}
	return fmt.Sprintf("%d", *v)
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

func flagValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"="), true
		}
	}
	return "", false
}

func requiredFlag(args []string, name string) (string, error) {
	v, ok := flagValue(args, name)
	if !ok || strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("%w: %s is required", model.ErrInvalid, name)
	}
	return v, nil
}

func repeatedFlag(args []string, name string) []string {
	var out []string
	for i, a := range args {
		if a == name && i+1 < len(args) {
			out = append(out, args[i+1])
		}
		if strings.HasPrefix(a, name+"=") {
			out = append(out, strings.TrimPrefix(a, name+"="))
		}
	}
	return out
}

func (c command) learningCommit(ctx context.Context, service *app.LearningService, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: commit input JSON file required", model.ErrInvalid)
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	var in app.CommitInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("decode learning commit input: %w", err)
	}
	got, err := service.Commit(ctx, in)
	if err != nil {
		return err
	}
	human := fmt.Sprintf("memory-commit=%s promoted=%d revised=%d invalidated=%d blocked=%d",
		got.ID, len(got.Additions), len(got.Revisions), len(got.Invalidations), len(got.BlockedLearning))
	// Refusals are part of the result, not a footnote. Printing them keeps the
	// operator aware of what did not become knowledge and why.
	for _, blocked := range got.BlockedLearning {
		human += "\n  blocked: " + blocked
	}
	return c.print(got, human)
}

func (c command) learningSearch(ctx context.Context, service *app.LearningService, args []string) error {
	project, err := requiredFlag(args, "--project")
	if err != nil {
		return err
	}
	q := learning.Query{
		ProjectID:      project,
		IncludeGeneral: hasFlag(args, "--general"),
		IncludeStale:   hasFlag(args, "--stale"),
	}
	if terms, ok := flagValue(args, "--terms"); ok {
		q.Terms = strings.Split(terms, ",")
	}
	got, err := service.Search(ctx, q)
	if err != nil {
		return err
	}
	lines := make([]string, 0, len(got))
	for _, r := range got {
		// Every line carries the uncertainty with the claim, so a reader
		// cannot take a stale or contested item for a current fact.
		line := fmt.Sprintf("%s [%s scope=%s clusters=%d usable=%t fresh=%t] %s",
			r.Item.ID, r.Item.State, r.Item.Scope, r.Clusters, r.Usable, r.Fresh, r.Item.Claim)
		if r.Contradicted {
			line += " (contradicted)"
		}
		lines = append(lines, line)
	}
	return c.print(got, strings.Join(lines, "\n"))
}

func (c command) learningContext(ctx context.Context, service *app.LearningService, args []string) error {
	project, err := requiredFlag(args, "--project")
	if err != nil {
		return err
	}
	q := learning.Query{ProjectID: project, IncludeGeneral: hasFlag(args, "--general")}
	if terms, ok := flagValue(args, "--terms"); ok {
		q.Terms = strings.Split(terms, ",")
	}
	got, err := service.Context(ctx, repeatedFlag(args, "--constraint"), q)
	if err != nil {
		return err
	}
	lines := make([]string, 0, len(got))
	for _, in := range got {
		lines = append(lines, fmt.Sprintf("%s binding=%t %s", in.Kind, in.Binding, in.Text))
	}
	return c.print(got, strings.Join(lines, "\n"))
}

// invalidateInput names the dependencies that changed. Invalidation is bound to
// a verification like every other memory commit, so the reason for the change is
// auditable rather than anonymous.
type invalidateInput struct {
	ID           string                `json:"memory_commit_id"`
	Verification string                `json:"verification_id"`
	Provenance   string                `json:"provenance"`
	Changed      []learning.Dependency `json:"changed_dependencies"`
}

func (c command) learningInvalidate(ctx context.Context, service *app.LearningService, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: invalidation input JSON file required", model.ErrInvalid)
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	var in invalidateInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("decode invalidation input: %w", err)
	}
	got, err := service.Invalidate(ctx, in.ID, in.Verification, in.Provenance, in.Changed)
	if err != nil {
		return err
	}
	return c.print(got, fmt.Sprintf("memory-commit=%s staled=%d", got.ID, len(got.Revisions)))
}

func (c command) learningRestore(ctx context.Context, service *app.LearningService, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: backup bundle JSON file required", model.ErrInvalid)
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	var bundle learning.ExportBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return fmt.Errorf("decode backup bundle: %w", err)
	}
	project, err := requiredFlag(args[1:], "--project")
	if err != nil {
		return err
	}
	got, err := service.PlanRestore(ctx, bundle, project, hasFlag(args[1:], "--general"))
	if err != nil {
		return err
	}
	lines := make([]string, 0, len(got))
	for _, p := range got {
		lines = append(lines, fmt.Sprintf("%s apply=%t %s", p.ItemID, p.Apply, p.Reason))
	}
	// The plan is printed, not applied. Restore is a reviewed operation.
	return c.print(got, strings.Join(lines, "\n"))
}
