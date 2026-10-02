package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/epistemic"
	"github.com/Zen1th53/marshal/internal/execution"
)

// fingerprintListLimit bounds how many failure signatures are shown.
const fingerprintListLimit = 20

type fingerprintGroup struct {
	fingerprint, normalized, lastStage string
	occurrences                        int
	tasks, runs                        map[string]bool
	lastSeen                           time.Time
}

// handleFingerprint recomputes failure fingerprints from the failures every
// run durably recorded. A fingerprint is a pure function of the failure
// text, so this is history, not the in-memory retry registry of one run.
func (h *CommandHandler) handleFingerprint(ctx context.Context) (string, error) {
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil {
		return "FAILURE FINGERPRINTS:\n  State: NOT_AVAILABLE (TUI is not connected to a runtime).", nil
	}
	service, err := a.execution()
	if err != nil {
		return "FAILURE FINGERPRINTS:\n  State: NOT_AVAILABLE (" + err.Error() + ").", nil
	}
	runs, err := service.Engine().ListRuns(ctx)
	if err != nil {
		return "", fmt.Errorf("read runs: %w; reopen the TUI and check /store", err)
	}
	groups := map[string]*fingerprintGroup{}
	failures := 0
	for _, run := range runs {
		for _, f := range run.Failures {
			failures++
			addFingerprint(groups, run, f)
		}
	}
	if failures == 0 {
		return fmt.Sprintf("FAILURE FINGERPRINTS (from %d recorded runs):\n  No failures recorded.", len(runs)), nil
	}
	ordered := make([]*fingerprintGroup, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].occurrences != ordered[j].occurrences {
			return ordered[i].occurrences > ordered[j].occurrences
		}
		return ordered[i].lastSeen.After(ordered[j].lastSeen)
	})
	var b strings.Builder
	fmt.Fprintf(&b, "FAILURE FINGERPRINTS (%d distinct across %d recorded failures in %d runs; recomputed from durable run records):\n", len(ordered), failures, len(runs))
	for i, g := range ordered {
		if i == fingerprintListLimit {
			fmt.Fprintf(&b, "  ... %d more not shown\n", len(ordered)-fingerprintListLimit)
			break
		}
		marker := ""
		if g.occurrences >= 2 {
			marker = "  REPEATED: blind retry should stop and escalate"
		}
		fmt.Fprintf(&b, "  %s  x%d  %d tasks, %d runs, last %s at %s%s\n", g.fingerprint, g.occurrences, len(g.tasks), len(g.runs), g.lastStage, g.lastSeen.Format("2006-01-02 15:04"), marker)
		fmt.Fprintf(&b, "    %s\n", g.normalized)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func addFingerprint(groups map[string]*fingerprintGroup, run execution.ExecutionRun, f execution.RunFailure) {
	fp, normalized := epistemic.FingerprintOf(f.Reason)
	g := groups[fp]
	if g == nil {
		// The signature is redacted and shortened before it is ever shown.
		line := strings.SplitN(RedactContent(normalized, nil), "\n", 2)[0]
		if len(line) > 120 {
			line = line[:117] + "..."
		}
		g = &fingerprintGroup{fingerprint: fp, normalized: line, tasks: map[string]bool{}, runs: map[string]bool{}}
		groups[fp] = g
	}
	g.occurrences++
	g.runs[run.RunID] = true
	if f.TaskID != "" {
		g.tasks[f.TaskID] = true
	}
	if f.Timestamp.After(g.lastSeen) {
		g.lastSeen, g.lastStage = f.Timestamp, f.Stage
	}
}
