//go:build !governed

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// proposalMCPRebind is one restarted Marshal chat taken over by the launch
// the Board minted for it.
type proposalMCPRebind struct {
	from, to string
	moved    []string
}

// retireProposalMCPForResume ends the lease of a Marshal chat the Board is
// about to restart in place. The lease stops answering at once, exactly as a
// revoke; only its open submissions are kept, for the launch that replaces
// it (takeRebindSourceLocked). Anything else is an ordinary revoke.
func (w *Workspace) retireProposalMCPForResume(a *activeTmuxAgent) {
	if a == nil {
		return
	}
	w.proposalMCP.mu.Lock()
	l := w.proposalMCP.leases[a.briefingDir]
	if l == nil || l.revoked || !l.active || w.proposalMCP.active != l || a.role != "marshal-chat" {
		w.proposalMCP.mu.Unlock()
		if l != nil {
			w.revokeProposalLease(l)
		}
		return
	}
	l.planning = nil
	l.revoked = true
	l.active = false
	w.proposalMCP.active = nil
	delete(w.proposalMCP.leases, l.dir)
	prior := w.proposalMCP.predecessor
	w.proposalMCP.predecessor = &proposalMCPPredecessor{id: l.id, owner: l.owner, provider: l.provider, at: time.Now()}
	if prior != nil && w.store != nil {
		_ = w.store.ExpireMCPLaunch(context.Background(), w.projectID, prior.id)
	}
	w.proposalMCP.mu.Unlock()
	l.cleanupOnce.Do(l.cleanup)
}

// takeRebindSourceLocked runs under proposalMCP.mu while l becomes the
// active Marshal launch. A chat the Board retired for its own in-place
// restart, of the same provider, hands its open submissions to l in the
// store, so the restarted chat sees them as its own. A superseded live
// lease, another provider, or a retirement too long ago expires as before.
// Authority moves by lease identity only: l was minted by the Board for the
// bound Marshal chat window, and the old lease is revoked before this runs.
func (w *Workspace) takeRebindSourceLocked(l *proposalMCPLease) (superseded *proposalMCPLease, rb *proposalMCPRebind) {
	var from *proposalMCPPredecessor
	expire := func(id string) {
		if w.store != nil {
			_ = w.store.ExpireMCPLaunch(context.Background(), w.projectID, id)
		}
	}
	if old := w.proposalMCP.active; old != nil && old != l {
		old.planning = nil
		old.active = false
		old.revoked = true
		superseded = old
		delete(w.proposalMCP.leases, old.dir)
		// A live chat replaced by another launch is not a restart the
		// Board made: its open work expires, as it always did.
		expire(old.id)
	}
	if pred := w.proposalMCP.predecessor; pred != nil {
		w.proposalMCP.predecessor = nil
		if superseded == nil && pred.provider == l.provider && time.Since(pred.at) < proposalMCPRebindWindow {
			from = pred
		} else {
			expire(pred.id)
		}
	}
	if from == nil || from.id == l.id {
		return superseded, nil
	}
	rb = &proposalMCPRebind{from: from.id, to: l.id}
	if w.store != nil {
		moved, err := w.store.RebindMCPLaunch(context.Background(), w.projectID, from.id, l.id, l.owner, l.provider)
		if err == nil {
			rb.moved = moved
		}
		// What stayed (memory, applying, or all of it on a failed move)
		// expires with its launch, as before.
		expire(from.id)
	}
	return superseded, rb
}

// rebindMarshalChatRequests moves the Board's open requests from the
// restarted chat's launch to the new one: same request IDs, so the resumed
// conversation can answer what it already saw, and each request it had been
// sent is sent once more. Answers are still checked against the live lease;
// the old launch can answer nothing, and its unapplied answers are dropped.
func (w *Workspace) rebindMarshalChatRequests(rb proposalMCPRebind) {
	now := time.Now()
	marker := "\x00" + rb.from + "\x00"
	rekey := func(k string) string { return strings.Replace(k, marker, "\x00"+rb.to+"\x00", 1) }
	w.permissions.mu.Lock()
	batch := w.permissions.queue.Take(false)
	for _, r := range batch {
		if r.SourceLaunch == rb.from {
			r.SourceLaunch = rb.to
		}
		w.permissions.queue.Add(r)
	}
	resent, open := 0, 0
	for _, p := range w.permissions.build.pending {
		if p.launchID != rb.from {
			continue
		}
		p.launchID = rb.to
		if p.request.SourceLaunch == rb.from {
			p.request.SourceLaunch = rb.to
		}
		if p.answer != nil {
			// An answer the old chat gave but the Board has not applied
			// is not carried: a changed launch authorizes nothing. The
			// question is asked again, under the same ID.
			p.answer, p.reason, p.occurrence = nil, "", ""
		}
		open++
		if p.delivered {
			p.delivered = false
			resent++
		}
		p.blocked = false
		p.extendForDelivery(now)
	}
	rekeyMap(w.permissions.outstanding, marker, rekey)
	rekeyMap(w.permissions.build.redeliver, marker, rekey)
	rekeyMap(w.permissions.build.awaiting, marker, rekey)
	rekeyMap(w.permissions.runProposals, marker, rekey)
	w.permissions.mu.Unlock()
	w.chatRebound(now, resent)
	if w.store != nil {
		_ = w.store.AppendEvent(context.Background(), nil, model.Event{ID: "chat-rebound-" + rb.to, Type: "MARSHAL_CHAT_REBOUND", ProjectID: w.projectID, Timestamp: now.UTC(), Data: map[string]any{
			"from_launch_id": rb.from, "to_launch_id": rb.to, "open_requests": open, "resent": resent, "carried_submissions": len(rb.moved),
		}})
	}
	w.RecordActivity(fmt.Sprintf("Marshal chat restarted and rebound to this project: %s re-sent, %s carried over.", count(resent, "open request", "open requests"), count(len(rb.moved), "submission", "submissions")))
}

func rekeyMap[V any](m map[string]V, marker string, rekey func(string) string) {
	for k, v := range m {
		if strings.Contains(k, marker) {
			delete(m, k)
			m[rekey(k)] = v
		}
	}
}

// liveOccurrenceMCP is the occurrence ID of the live launch's submission: a
// submission carried from a restarted chat keeps the ID it was admitted
// under; a new one gets the launch's own.
func (w *Workspace) liveOccurrenceMCP(ctx context.Context, l *proposalMCPLease, submission string) string {
	if w.store != nil {
		if p, err := w.store.MCPProposalBySubmission(ctx, w.projectID, l.id, submission); err == nil {
			return p.ID
		}
	}
	return occurrenceMCP(l.id, submission)
}

// chatAuthorityBound reports whether a live Marshal lease is bound to this
// project's chat: only then can the chat answer the Board's requests.
func (w *Workspace) chatAuthorityBound() bool { return w.proposalLaunchID() != "" }
