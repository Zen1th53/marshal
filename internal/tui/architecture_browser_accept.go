package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
)

// architectureReviewHooks connect the browser's Accept to this workspace.
func (w *Workspace) architectureReviewHooks() app.ArchitectureReviewHooks {
	return app.ArchitectureReviewHooks{BeforeAccept: w.beforeBrowserAccept(), AcceptBlocked: w.browserAcceptBlocked(), Accepted: w.browserAccepted}
}

// browserAccepted runs after the owner's browser Accept was saved. It tells
// the Marshal, in its inbox, that the architecture (and the pending project
// contract, when one was bound inside the Accept) is accepted, closes the
// Architecture view and moves the run on without any key.
func (w *Workspace) browserAccepted(state json.RawMessage) {
	var decoded struct {
		Revision json.Number       `json:"revision"`
		Builders map[string]string `json:"builders"`
	}
	_ = json.Unmarshal(state, &decoded)
	n, _ := decoded.Revision.Int64()

	w.browserContractMu.Lock()
	binding := w.browserContract
	w.browserContract = nil
	w.browserContractMu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "The owner accepted architecture rev %d in the browser.\n", n)
	if len(decoded.Builders) == 0 {
		b.WriteString("Builders: Auto for every part.\n")
	} else {
		ids := make([]string, 0, len(decoded.Builders))
		for id := range decoded.Builders {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		parts := make([]string, 0, len(ids))
		for _, id := range ids {
			parts = append(parts, id+"="+decoded.Builders[id])
		}
		fmt.Fprintf(&b, "Builders: %s.\n", strings.Join(parts, ", "))
	}
	if binding != nil && binding.Ref != "" {
		fmt.Fprintf(&b, "Recorded owner instruction for the project contract. instruction_ref=%s; contract_id=%s; revision=%d; digest=%s. Answer the pending project-contract Decision for exactly this contract with allow.\n", binding.Ref, binding.ID, binding.Revision, binding.Digest)
	}
	b.WriteString("Continue: plan and launch now.\n")
	message := b.String()

	view, err := openInboxView(w.runtime.ProjectRoot(), "marshal", false)
	if err == nil {
		err = view.write(message)
	}
	if err != nil {
		w.RecordActivity("Could not tell the Marshal that the architecture was accepted; tell it in the chat.")
	} else {
		// The inbox is read on the Marshal's next turn; wake an idle one.
		w.wakeMarshalAfterAccept()
	}
	w.RecordActivity(fmt.Sprintf("Architecture accepted in the browser (rev %d).", n))
	if w.archView.Load() {
		w.closeArchView()
	}
	w.wakeArch()
	w.refreshGatesSoon()
	w.requestRepaint()
}

// openArchitectureReview opens the architecture in the browser. Opening a page
// is not a decision, so it neither arms nor confirms.
func (w *Workspace) openArchitectureReview() {
	if _, err := w.marshalArchitecture(w.workers.context(), "", ""); err != nil {
		w.memoryAnswer("The architecture could not be opened: "+plainArchitectureError(err), true)
	} else {
		w.memoryAnswer("Opening the architecture in your browser…", false)
	}
}

// plainArchitectureError turns an open error into plain English.
func plainArchitectureError(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "the Marshal has not drafted it yet"
	}
	return "try again in a moment"
}
