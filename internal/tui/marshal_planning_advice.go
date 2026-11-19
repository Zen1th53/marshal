package tui

import (
	"fmt"
	"strings"
)

// Planning advice belongs in the Marshal's inbox. It creates no owner
// notification, permission popup, approval requirement or execution gate.
func writeMarshalPlanningAdvice(root, runID string, warnings []string) error {
	if len(warnings) == 0 {
		return nil
	}
	view, err := openInboxView(root, "marshal", false)
	if err != nil {
		return err
	}
	return view.write(fmt.Sprintf("## runtime · planning check\n\nRun: %s\n%s\n", runID, strings.Join(warnings, "\n")))
}
