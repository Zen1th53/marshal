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
		fact := marshal.ProviderReadiness{Installed: p.Installed, SignedIn: p.SignedIn, AuthUnknown: p.State == "unchecked" || p.State == "checking"}
		if p.State == "limit" {
			fact.Unavailable = p.Reason
		}
		providers[name] = fact
	}
	policy := in.Probe.Settings.ReviewPolicy
	return marshal.ComputeReadiness(in.marshalProvider(), providers, policy)
}

func (w *Workspace) executionReadiness() marshal.Readiness {
	in := w.v2MarshalInput(time.Now())
	return in.readiness()
}

// applyReadiness is shared by the terminal Board and its web snapshot.
func applyReadiness(b *vm.Board, r marshal.Readiness) {
	if r.Blocker == "" {
		return
	}
	if b.Top.State == "READY" {
		b.Top.State, b.Top.StateTone = "YOUR TURN", vm.ToneWarn
	}
	b.NowLine.FromYou = r.Blocker
	b.Inbox.Lines = append([]string{r.Blocker}, b.Inbox.Lines...)
	b.Inbox.Refs = append([]string{"readiness"}, b.Inbox.Refs...)
	b.Inbox.Count++
	b.Top.Inbox++
	b.Inbox.Next = r.Blocker
}
