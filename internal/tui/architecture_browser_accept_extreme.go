//go:build !governed

package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
)

// beforeBrowserAccept records the owner's instruction for the pending project
// contract inside the browser Accept. The acceptance is refused and nothing is
// saved when no instruction can be bound.
func (w *Workspace) beforeBrowserAccept() func(context.Context, json.RawMessage) error {
	return func(ctx context.Context, state json.RawMessage) error {
		var sel struct {
			ID       string `json:"architecture_id"`
			Revision int64  `json:"revision"`
			Digest   string `json:"digest"`
		}
		if err := json.Unmarshal(state, &sel); err != nil {
			return errors.New("The Marshal's project summary is not ready yet. Try again in a moment.")
		}
		session := w.boundMarshalArchitectureSource()
		if session == "" {
			return errors.New("Open the Marshal chat in your terminal, then press Accept again.")
		}
		ref := fmt.Sprintf("browser:%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%d", sel.ID, sel.Revision, sel.Digest, time.Now().UnixNano()))))
		binding, _, err := w.recordProjectContractInstruction(ctx, session, ref, store.BrowserAcceptanceInstruction, time.Now().UTC())
		if err != nil {
			return errors.New("The Marshal's project summary is not ready yet. Try again in a moment.")
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
