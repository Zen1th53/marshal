package tui

import (
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

func (in v2MarshalIn) readiness() marshal.Readiness {
	providers := map[string]marshal.ProviderReadiness{}
	for _, name := range v2ProviderOrder {
		p := in.provider(name)
		// Sign-in is known false only when a check said so; a provider at a
		// limit or not checked yet has unknown sign-in.
		fact := marshal.ProviderReadiness{Installed: p.Installed, SignedIn: p.SignedIn, AuthUnknown: !p.SignedIn && p.State != "not signed in" && p.State != "not installed"}
		if p.State == "limit" {
			fact.Unavailable = p.Reason
		}
		providers[name] = fact
	}
	policy := in.Probe.Settings.ReviewPolicy
	return marshal.ComputeReadiness(in.marshalProvider(), providers, policy)
}

// executionReadiness is the verdict the Board, web board and status line
// show, and whether the first provider probe has finished. Before it has,
// nothing is known yet, so no blocker is named rather than a guessed one;
// approvals read in.readiness() directly and stay conservative.
func (w *Workspace) executionReadiness() (marshal.Readiness, bool) {
	in := w.v2MarshalInput(time.Now())
	if !in.Probed {
		return marshal.Readiness{}, false
	}
	return in.readiness(), true
}

// applyReadiness is shared by the terminal Board and its web snapshot.
// Before the first probe the top bar says CHECKING, never READY.
func applyReadiness(b *vm.Board, r marshal.Readiness, probed bool) {
	if !probed && b.Top.State == "READY" {
		b.Top.State, b.Top.StateTone = "CHECKING", vm.ToneMuted
	}
	if r.Blocker == "" {
		return
	}
	if b.Top.State == "READY" {
		b.Top.State, b.Top.StateTone = "YOUR TURN", vm.ToneWarn
	}
	// A decision already waiting for the owner keeps the From-you line; the
	// blocker still leads the inbox.
	if b.NowLine.FromYouOK || b.NowLine.FromYou == "" {
		b.NowLine.FromYou = r.Blocker
	}
	b.Inbox.Lines = append([]string{r.Blocker}, b.Inbox.Lines...)
	b.Inbox.Refs = append([]string{"readiness"}, b.Inbox.Refs...)
	b.Inbox.Count++
	b.Top.Inbox++
	b.Inbox.Next = r.Blocker
}
