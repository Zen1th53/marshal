package tui

import (
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
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
	applyReadiness(&b, r)
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
	want := w.executionReadiness().Blocker
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
