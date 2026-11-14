//go:build !governed

package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

// startArchitectureReviewSession opens the review on the pending project
// contract when one waits for the owner (a first draft or a new revision),
// otherwise the model-drafted review.
func (w *Workspace) startArchitectureReviewSession(ctx context.Context, rt *app.Runtime, provider, goal string) (*app.ArchitectureReviewSession, error) {
	if strings.TrimSpace(goal) == "" {
		session, used, err := rt.StartArchitectureReviewFromContract(ctx, provider, w.sessionID, w.proposalLaunchID(), w.architectureReviewHooks())
		if used || err != nil {
			return session, err
		}
	}
	session, err := rt.StartArchitectureReviewForSession(ctx, provider, goal, w.sessionID, w.architectureReviewHooks())
	if errors.Is(err, projectcontext.ErrReviewPending) && w.store != nil {
		// A drafted review needs an accepted project contract, and none is
		// accepted or waiting: only the Marshal's summary can start it.
		if _, accepted := w.store.ReadAcceptedProjectContract(ctx, rt.ProjectContractBinding()); errors.Is(accepted, projectcontext.ErrReviewPending) {
			if w.askMarshalForProjectSummary() {
				return nil, fmt.Errorf("%w; it was asked to send it now, then open the architecture again", errArchitectureAwaitsSummary)
			}
			return nil, fmt.Errorf("%w; open the Marshal chat so it can send it, then open the architecture again", errArchitectureAwaitsSummary)
		}
	}
	return session, err
}

// projectSummaryRequest asks the Marshal for the project contract the
// architecture view is built from. It grants and accepts nothing.
const projectSummaryRequest = marshalNotePrefix + "The owner asked to see the architecture, but no project summary is waiting. Read every marshal_project_contract_inventory page, then send the architecture with marshal_project_contract_submit. If it is refused, the error says what to fix."

// askMarshalForProjectSummary sends projectSummaryRequest when a Marshal chat
// is open, and reports whether it did.
func (w *Workspace) askMarshalForProjectSummary() bool {
	if w.sayPane() == "" && !w.nativeChatShown() {
		return false
	}
	w.noticeToMarshal(w.workers.context(), projectSummaryRequest)
	return true
}

// browserAcceptNudge wakes an idle Marshal after a recorded browser Accept.
// It is not an acceptance and carries no binding: the instruction itself is
// only in the inbox, and no chat text accepts anything.
const browserAcceptNudge = marshalNotePrefix + "The owner accepted the architecture in the browser. Check your inbox."

// wakeMarshalAfterAccept sends the nudge through the same path the ask card
// uses for its answer, only when a Marshal chat is open; otherwise the inbox
// note waits for the Marshal's next turn.
func (w *Workspace) wakeMarshalAfterAccept() {
	if w.sayPane() == "" && !w.nativeChatShown() {
		return
	}
	w.noticeToMarshal(w.workers.context(), browserAcceptNudge)
}

// browserContractChanged refuses an Accept of a page built from a contract
// the Marshal has since replaced.
var browserContractChanged = errors.New("The Marshal sent a newer project summary. Open the architecture again from your terminal, then press Accept.")

// beforeBrowserAccept records the owner's instruction for the pending project
// contract inside the browser Accept. The acceptance is refused and nothing is
// saved when no instruction can be bound.
func (w *Workspace) beforeBrowserAccept() func(context.Context, json.RawMessage) error {
	return func(ctx context.Context, state json.RawMessage) error {
		var sel struct {
			ID           string `json:"architecture_id"`
			Revision     int64  `json:"revision"`
			Digest       string `json:"digest"`
			Architecture struct {
				Source *struct {
					ID       string `json:"id"`
					Revision int64  `json:"revision"`
					Digest   string `json:"digest"`
				} `json:"source_contract"`
			} `json:"architecture"`
		}
		if err := json.Unmarshal(state, &sel); err != nil {
			return errors.New("The Marshal's project summary is not ready yet. Try again in a moment.")
		}
		session := w.boundMarshalArchitectureSource()
		if session == "" {
			return errors.New("Open the Marshal chat in your terminal, then press Accept again.")
		}
		// A page built from a pending contract accepts exactly that contract.
		src := sel.Architecture.Source
		if src != nil {
			c, err := w.readPendingContract(ctx)
			if err != nil {
				return errors.New("The Marshal's project summary is not ready yet. Try again in a moment.")
			}
			if c != nil && (c.ID != src.ID || c.Revision != src.Revision || c.Digest != src.Digest) {
				return browserContractChanged
			}
		}
		ref := fmt.Sprintf("browser:%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%d", sel.ID, sel.Revision, sel.Digest, time.Now().UnixNano()))))
		binding, _, err := w.recordProjectContractInstruction(ctx, session, ref, store.BrowserAcceptanceInstruction, time.Now().UTC())
		if err != nil {
			return errors.New("The Marshal's project summary is not ready yet. Try again in a moment.")
		}
		if src != nil && binding.Ref != "" && (binding.ID != src.ID || binding.Revision != src.Revision || binding.Digest != src.Digest) {
			return browserContractChanged
		}
		if binding.Ref != "" {
			w.browserContractMu.Lock()
			b := binding
			w.browserContract = &b
			w.browserContractMu.Unlock()
		}
		return nil
	}
}

// browserAcceptBlocked reports why Accept is not yet available: the Marshal has
// not prepared the project summary (no pending or accepted project contract).
// A transient read error never blocks Accept; BeforeAccept still enforces.
func (w *Workspace) browserAcceptBlocked() func() string {
	const notReady = "The Marshal is still preparing the project summary."
	var mu sync.Mutex
	var at time.Time
	var cached string
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		if !at.IsZero() && time.Since(at) < 5*time.Second {
			return cached
		}
		ctx, cancel := context.WithTimeout(w.workers.context(), 2*time.Second)
		defer cancel()
		reason := notReady
		if c, err := w.readPendingContract(ctx); err != nil || c != nil {
			reason = ""
		} else if _, err := w.store.ReadAcceptedProjectContract(ctx, w.runtime.ProjectContractBinding()); err == nil {
			reason = ""
		}
		cached, at = reason, time.Now()
		return reason
	}
}
