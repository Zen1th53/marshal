package tui

import (
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
	"github.com/Zen1th53/marshal/internal/usage"
	"strings"
	"testing"
	"time"
)

func TestReadinessSurfacesUsePlannerVerdict(t *testing.T) {
	in := v2MarshalIn{Probed: true, Probe: v2Probe{Lead: "codex", Installed: map[string]bool{"codex": true}}, SignedOut: map[string]string{"codex": "not signed in"}}
	r := in.readiness()
	if got := pickerReadiness(in); got != r.Blocker {
		t.Fatalf("picker %q, verdict %q", got, r.Blocker)
	}
	b := vm.Board{Top: vm.TopBar{State: "READY"}}
	applyReadiness(&b, r, true)
	if b.Top.State == "READY" || b.NowLine.FromYou != r.Blocker || b.Inbox.Lines[0] != r.Blocker || b.Inbox.Count != 1 || b.Top.Inbox != 1 {
		t.Fatalf("%+v", b)
	}
	ready := buildV2Ready(in)
	if len(ready.Checks) == 0 || ready.Checks[0].Text != r.Blocker {
		t.Fatalf("%+v", ready)
	}
	header := runtimeStateSegment(UIState{ReadinessBlocker: r.Blocker}, NewTheme(ThemeNoColor, false, false))
	if !strings.Contains(header, r.Blocker) || strings.Contains(header, "READY") {
		t.Fatal(header)
	}
}

func TestReadinessUsesPinnedPolicy(t *testing.T) {
	r := marshal.ComputeReadiness("codex", map[string]marshal.ProviderReadiness{"codex": {Installed: true, SignedIn: true}}, marshal.ReviewPolicy{FinalModel: marshal.FinalModelOff, CrossReview: marshal.CrossReviewOff})
	if !r.NeedsConfirmation || r.Blocker != "" || !strings.Contains(r.Guarantee, "Review is Off") || !strings.Contains(r.Guarantee, "Checks-only") {
		t.Fatalf("%+v", r)
	}
}

func TestWebAndTerminalFramesShareReadiness(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.v2m.probe = v2Probe{Lead: "codex", Installed: map[string]bool{"codex": true}}
	w.v2m.at = time.Now()
	w.v2m.key = "\x00"
	w.endOpening()
	r, probed := w.executionReadiness()
	want := r.Blocker
	if !probed {
		t.Fatal("the recorded probe was not used")
	}
	for name, frame := range map[string]vm.Board{"terminal": w.boardV2(), "web": w.webBoardFrame()} {
		if frame.Top.State == "READY" || frame.NowLine.FromYou != want || len(frame.Inbox.Lines) == 0 || frame.Inbox.Lines[0] != want {
			t.Fatalf("%s: %+v", name, frame)
		}
	}
}

func TestApprovalCardStatesEffectiveGuarantee(t *testing.T) {
	for _, policy := range []marshal.ReviewPolicy{{}, {CrossReview: marshal.CrossReviewOff}, {FinalModel: marshal.FinalModelOff}} {
		r := marshal.ComputeReadiness("codex", map[string]marshal.ProviderReadiness{"codex": {Installed: true, SignedIn: true}}, policy)
		scope, reason := approvalGuaranteeText("Plan", "Please check this plan.", r)
		if !strings.Contains(scope, r.Guarantee) || !strings.Contains(scope, Edition()) || !strings.Contains(reason, "Confirm this guarantee or deny") {
			t.Fatalf("%s %s", scope, reason)
		}
	}
}

// probedProvidersReady records a finished provider probe in which lead plans
// and every named provider is installed and signed in, so the readiness
// verdict can reach full guarantees without starting a provider.
func probedProvidersReady(w *Workspace, lead string, names ...string) {
	installed := map[string]bool{}
	var quotas []usage.Quota
	for _, name := range names {
		installed[name] = true
		quotas = append(quotas, usage.Quota{Provider: name, Known: true, AsOf: time.Now()})
	}
	w.v2m.mu.Lock()
	w.v2m.probe = v2Probe{Lead: lead, Installed: installed, Settings: marshal.DefaultSettings(), HasSettings: true}
	w.v2m.at = time.Now()
	w.v2m.key = "\x00"
	w.v2m.mu.Unlock()
	w.usage.mu.Lock()
	w.usage.quotas = quotas
	w.usage.read = true
	w.usage.mu.Unlock()
}

// idleBeforePlannerReady is whether a frame's top bar shows an idle project
// whose planner is not known to run yet: CHECKING before the first provider
// probe, YOUR TURN after it, never READY.
func idleBeforePlannerReady(frame string) bool {
	return (strings.Contains(frame, " CHECKING ") || strings.Contains(frame, " YOUR TURN ")) && !strings.Contains(frame, " READY ")
}

func TestBoardSaysCheckingBeforeTheFirstProbe(t *testing.T) {
	b := vm.Board{Top: vm.TopBar{State: "READY"}, NowLine: vm.NowLine{FromYou: "nothing", FromYouOK: true}}
	applyReadiness(&b, marshal.Readiness{}, false)
	if b.Top.State != "CHECKING" || b.Inbox.Count != 0 || b.NowLine.FromYou != "nothing" {
		t.Fatalf("%+v", b)
	}
}

func TestReadinessKeepsAWaitingDecisionOnFromYou(t *testing.T) {
	r := marshal.ComputeReadiness("", nil, marshal.ReviewPolicy{})
	b := vm.Board{Top: vm.TopBar{State: "YOUR TURN"}, NowLine: vm.NowLine{FromYou: "changed files: keep or undo them"}}
	applyReadiness(&b, r, true)
	if b.NowLine.FromYou != "changed files: keep or undo them" || len(b.Inbox.Lines) == 0 || b.Inbox.Lines[0] != r.Blocker {
		t.Fatalf("%+v", b)
	}
}
