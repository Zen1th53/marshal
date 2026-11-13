package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/statepath"
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
	w.clearAnswer(architectureOpening)
	w.clearAnswer(architectureOpened)
	w.RecordActivity(fmt.Sprintf("Architecture accepted in the browser (rev %d).", n))
	w.planning.Store(true)
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
	// The answer is set first: the page may open before marshalArchitecture
	// returns, and its clear must find this text to remove.
	w.memoryAnswer(architectureOpening, false)
	if _, err := w.marshalArchitecture(w.workers.context(), "", ""); err != nil {
		w.memoryAnswer("The architecture could not be opened: "+plainArchitectureError(err), true)
	}
}

// architectureOpening is the footer answer while the browser opens; it is
// replaced once the page has opened (architectureOpened) or could not open
// (architectureNoBrowser), and cleared once the owner accepted it, so it
// never outlives what it describes.
const architectureOpening = "Opening the architecture in your browser…"

// architectureURLFile is where the review's private link is kept, relative
// to the project. The link carries the page's key, so it is named, never
// written to Activity.
var architectureURLFile = filepath.Join(statepath.DirName, "architecture", "review", "url.txt")

// architectureOpened is the footer answer once the browser was asked to open
// the page; it stays until the owner accepts or does something else.
var architectureOpened = "The architecture is open in your browser. No page? Open the link in " + architectureURLFile + "."

// architectureNoBrowser is the footer answer when no browser could be opened.
var architectureNoBrowser = "Your browser could not be opened. Open the link in " + architectureURLFile + " to review the architecture."

// architectureShownURL is the review's address without its key, for Activity.
func architectureShownURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "the review page"
	}
	return u.Scheme + "://" + u.Host + "/"
}

// architectureOpenedNote and architectureFailedNote are the Activity lines for
// an open that worked or failed: the address without its key, and where the
// full link is.
func architectureOpenedNote(raw string) string {
	return "Architecture view opened in your browser: " + architectureShownURL(raw) + " (the full link is in " + architectureURLFile + ")."
}

func architectureFailedNote(raw string) string {
	return "Architecture view: your browser could not be opened. Open the link in " + architectureURLFile + " (" + architectureShownURL(raw) + ")."
}

// architectureOpenResult replaces the "Opening…" answer, while it is still
// shown, with what happened.
func (w *Workspace) architectureOpenResult(err error) {
	text, isError := architectureOpened, false
	if err != nil {
		text, isError = architectureNoBrowser, true
	}
	w.mu.Lock()
	still := w.answer == architectureOpening
	w.mu.Unlock()
	if still {
		w.memoryAnswer(text, isError)
	}
}

// clearAnswer removes the footer answer only while it is still text, so a
// newer answer to something else is never wiped.
func (w *Workspace) clearAnswer(text string) {
	w.mu.Lock()
	cleared := w.answer == text
	if cleared {
		w.answer, w.answerIsError = "", false
		if w.state.LastOutput == text {
			w.state.LastOutput, w.state.LastOutputIsError = "", false
		}
	}
	w.mu.Unlock()
	if cleared {
		w.outputSeq.Add(1)
		w.requestRepaint()
	}
}

// errArchitectureAwaitsSummary: no architecture can be drawn yet, because no
// project contract is accepted and the Marshal has not sent one to review.
var errArchitectureAwaitsSummary = errors.New("the Marshal has not sent the project summary yet")

// plainArchitectureError turns an open error into plain English.
func plainArchitectureError(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "the Marshal has not drafted it yet"
	}
	if errors.Is(err, errArchitectureAwaitsSummary) {
		return err.Error() + "."
	}
	return "try again in a moment"
}
