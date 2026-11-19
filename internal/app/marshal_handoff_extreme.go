//go:build !governed

package app

import (
	"context"
	"fmt"
	"strings"
)

// readHandoffContract reads the accepted and/or pending project contract for
// the active-goal handoff. It is read-only data and confers no planning proof.
func (r *Runtime) readHandoffContract(ctx context.Context) (acceptedSummary string, accepted bool, pendingSummary string, pending bool) {
	binding := r.ProjectContractBinding()
	if c, err := r.store.ReadAcceptedProjectContract(ctx, binding); err == nil {
		accepted = true
		parts := []string{fmt.Sprintf("accepted (%s, rev %d)", cleanHandoffLine(c.Contract.ID, 60), c.Contract.Revision)}
		if p := cleanHandoffLine(c.Contract.Purpose, 200); p != "" {
			parts = append(parts, p)
		}
		if len(c.Contract.Components) > 0 {
			names := make([]string, 0, min(len(c.Contract.Components), 10))
			for i, comp := range c.Contract.Components {
				if i >= 10 {
					break
				}
				if id := cleanHandoffLine(comp.ID, 40); id != "" {
					names = append(names, id)
				}
			}
			if len(names) > 0 {
				suffix := ""
				if len(c.Contract.Components) > len(names) {
					suffix = fmt.Sprintf(" (+%d more)", len(c.Contract.Components)-len(names))
				}
				parts = append(parts, "components: "+strings.Join(names, ", ")+suffix)
			}
		}
		acceptedSummary = strings.Join(parts, "; ")
	}
	// Pass launch="" so a contract drafted by the previous Marshal before a
	// provider switch is still surfaced as pending.
	if p, found, err := r.store.ReadPendingProjectContract(ctx, binding, ""); err == nil && found {
		pending = true
		pendingSummary = fmt.Sprintf("draft submitted (%s, rev %d), awaiting browser Accept", cleanHandoffLine(p.ID, 60), p.Revision)
		if purpose := cleanHandoffLine(p.Purpose, 180); purpose != "" {
			pendingSummary += "; " + purpose
		}
	}
	return acceptedSummary, accepted, pendingSummary, pending
}
