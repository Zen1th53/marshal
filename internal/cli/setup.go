package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/startup"
)

// setup reports readiness. It is strictly an assessment: it has no path that
// writes anything, which is what distinguishes it from Doctor's repair.
func (c *command) setup(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] != "status" {
		return fmt.Errorf("%w: unknown setup subcommand %s", model.ErrInvalid, args[0])
	}
	assessment := startup.Assess(ctx, startup.NewSystemProber(), c.startupEnvironment())

	if c.json {
		return c.print(assessment, "")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Readiness: %s\n\n", assessment.Phase)
	for _, check := range assessment.Checks {
		fmt.Fprintf(&b, "  [%-15s] %-24s %s\n", check.Status, check.ID, check.Summary)
		if check.Impact != "" && !check.Status.Healthy() && check.Status != startup.StatusOptional {
			fmt.Fprintf(&b, "  %-18s%-24s %s\n", "", "", check.Impact)
		}
	}

	// What is unavailable is stated positively rather than left for the user
	// to infer from a list of statuses.
	fmt.Fprintf(&b, "\nAvailable: %s\n", joinCapabilities(assessment.Capabilities))
	if blocking := assessment.Blocking(); len(blocking) > 0 {
		b.WriteString("\nWork cannot run until these are resolved:\n")
		for _, check := range blocking {
			fmt.Fprintf(&b, "  - %s\n    %s\n", check.Summary, check.Remedy)
		}
	}
	fmt.Fprint(c.stdout, b.String())
	return nil
}

func joinCapabilities(capabilities []startup.Capability) string {
	if len(capabilities) == 0 {
		return "nothing"
	}
	names := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		names = append(names, string(capability))
	}
	return strings.Join(names, ", ")
}
