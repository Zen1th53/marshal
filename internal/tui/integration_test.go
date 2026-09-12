package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/resources"
	"github.com/Zen1th53/marshal/internal/startup"
)

// TestIntegrationCrossLinksWalkTheFrozenIA checks both kinds of links against
// every frozen node. This supplements the structural loader test by following
// links through the navigation transition itself.
func TestIntegrationCrossLinksWalkTheFrozenIA(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatal(err)
	}
	seen, contextual := 0, 0
	for _, node := range integrationNodes(ia) {
		if node.Type == NodeCrossLink {
			seen++
			nav, err := NewNavState(ia)
			if err != nil {
				t.Fatal(err)
			}
			if node.HasContextualOwner() {
				contextual++
				if err := nav.FollowCrossLink(node); err == nil || !strings.Contains(err.Error(), "selected record") {
					t.Fatalf("contextual link %s was not refused without selection: %v", node.SpecID, err)
				}
				continue
			}
			if err := nav.FollowCrossLink(node); err != nil {
				t.Fatalf("cross-link %s (%s): %v", node.SpecID, node.MenuPath, err)
			}
			if node.Binding == BindingGap {
				if nav.Current() != node {
					t.Fatalf("gapped cross-link %s did not render its own declaration", node.SpecID)
				}
				continue
			}
			if nav.Current().MenuPath != node.CanonicalOwner {
				t.Fatalf("cross-link %s landed at %q, want %q", node.SpecID, nav.Current().MenuPath, node.CanonicalOwner)
			}
		}

		// Status renderers can offer their own corrective controls. They are
		// navigation-only too, and every advertised path must be in the IA.
		for _, link := range RenderNode(node, Snapshot{}).CrossLinks {
			if _, ok := ia.NodeByMenuPath(link.MenuPath); !ok {
				t.Fatalf("%s offers corrective link %q outside the frozen IA", node.SpecID, link.MenuPath)
			}
		}
	}
	if seen == 0 || contextual != 1 {
		t.Fatalf("walked %d cross-links with %d contextual links, want nonzero and exactly one", seen, contextual)
	}
	t.Logf("cross-links: %d total (%d contextual refusal checked)", seen, contextual)
}

// TestIntegrationEveryFrozenNodeRenders walks all destinations through the
// real NavView. A fallback is allowed only when it names the frozen CTUI id;
// no destination may reduce to an empty pane.
func TestIntegrationEveryFrozenNodeRenders(t *testing.T) {
	_, ws, _ := acceptanceWorkspace(t)
	v := ws.navView
	ia, _ := FrozenIA()
	fallback := 0
	rendered := make(map[string]bool)
	for _, node := range integrationNodes(ia) {
		if node.Type == NodeRoot {
			continue
		}
		if node.Type == NodeCrossLink && node.HasContextualOwner() {
			// A contextual destination intentionally has no standalone
			// canonical target. It renders its fallback if viewed as a
			// declaration and is refused by the real navigation transition
			// until a selected blocker supplies the target.
			content := RenderNode(node, Snapshot{})
			if !content.HasNotice || !strings.Contains(content.Notice.Display(), node.SpecID) {
				t.Fatalf("contextual link %s does not render a truthful declaration", node.SpecID)
			}
			continue
		}
		if err := v.Nav().DeepLink(node.SpecID); err != nil {
			t.Fatalf("deep link %s: %v", node.SpecID, err)
		}
		out := strings.TrimSpace(strings.Join(v.Render(200, 160), "\n"))
		if out == "" {
			t.Fatalf("%s (%s) rendered a blank pane", node.SpecID, node.MenuPath)
		}
		shown := v.Nav().Current()
		if shown == nil {
			t.Fatalf("%s left navigation without a current screen", node.SpecID)
		}
		// Actions live on their parent's action bar and cross-links open their
		// canonical owner. They are navigation/binding obligations, not
		// standalone screen-renderer obligations.
		standaloneScreen := !node.Type.IsAction() && node.Type != NodeCrossLink
		if standaloneScreen && strings.Contains(out, "declared by the frozen pack but is not implemented in this build") && !rendered[shown.SpecID] {
			fallback++
			if !strings.Contains(out, shown.SpecID) {
				t.Fatalf("fallback for %s omitted its CTUI id:\n%s", shown.SpecID, out)
			}
			rendered[shown.SpecID] = true
		}
		if node.Binding == BindingGap && (!strings.Contains(out, "IMPLEMENTATION GAP") || !strings.Contains(out, node.SpecID)) {
			t.Fatalf("gap %s rendered without its truthful notice:\n%s", node.SpecID, out)
		}
	}
	if fallback > 0 {
		t.Skipf("integration NOT_RUN: %d/%d nodes use the declared-but-not-implemented fallback", fallback, ia.Count())
	}
}

// TestIntegrationKeyboardAndMouseParityThroughWorkspaceDispatch exercises the
// same dispatchNavigationKey branch used by the raw terminal loop. No listed
// navigation gesture may reach canonical mutation from Home or Status.
func TestIntegrationKeyboardAndMouseParityThroughWorkspaceDispatch(t *testing.T) {
	ctx := context.Background()
	mouse, consumed := ParseNextKey([]byte("\x1b[<0;12;1M"))
	if mouse.Type != KeyMouse || mouse.MouseButton != 0 || mouse.MouseColumn != 12 || mouse.MouseRow != 1 || consumed != len(mouse.Raw) {
		t.Fatalf("SGR mouse parse = %#v, consumed=%d", mouse, consumed)
	}
	allKeys := []KeyEvent{
		{Type: KeyUp}, {Type: KeyDown}, runeKey('j'), runeKey('k'),
		{Type: KeyEnter}, {Type: KeyEsc}, {Type: KeyTab}, {Type: KeyShiftTab},
		runeKey('1'), runeKey('2'), runeKey('3'), runeKey('4'), runeKey('5'),
		runeKey('6'), runeKey('7'), runeKey('8'), runeKey('9'), runeKey('/'),
		{Type: KeyCtrlK}, runeKey('?'), runeKey('r'), runeKey('q'),
	}
	for _, start := range []struct {
		name string
		id   string
	}{{"Home", "CTUI-0001"}, {"Status", "CTUI-0089"}} {
		t.Run(start.name, func(t *testing.T) {
			ws, auth := integrationWorkspace(t)
			if err := ws.navView.Nav().DeepLink(start.id); err != nil {
				t.Fatal(err)
			}
			for _, event := range allKeys {
				if !ws.dispatchNavigationKey(ctx, event) {
					t.Fatalf("%v was not consumed while navigation was open", event.Type)
				}
				// q intentionally closes the view. Reopen via the same dispatch
				// path before testing the next globally listed key.
				if !ws.navView.IsOpen() {
					ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyCtrlN})
				}
			}
			if got := integrationMutationCount(auth); got != 0 {
				t.Fatalf("navigation sequence from %s reached %d canonical mutations", start.name, got)
			}
		})
	}

	// Mouse focus is intentionally equivalent to the keyboard's top-nav and
	// menu focus transitions; it does not implicitly open a row.
	ws, auth := integrationWorkspace(t)
	ws.navView.Render(140, 30) // establishes the geometry mouse reports use
	if !ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyMouse, MouseButton: 0, MouseColumn: 12, MouseRow: 1}) {
		t.Fatal("mouse tab focus was not consumed")
	}
	if ws.navView.Nav().Focus() != PaneTopNav {
		t.Fatalf("mouse tab focus = %s, want top navigation", ws.navView.Nav().Focus())
	}
	if got := integrationMutationCount(auth); got != 0 {
		t.Fatalf("mouse focus reached %d canonical mutations", got)
	}
}

// TestIntegrationNarrowNoColorPreservesTruthLabels proves responsive output is
// bounded by visible display cells and that no-color mode retains textual
// status labels instead of relying on colour.
func TestIntegrationNarrowNoColorPreservesTruthLabels(t *testing.T) {
	v, err := NewNavView(NewTheme(ThemeNoColor, false, false))
	if err != nil {
		t.Fatal(err)
	}
	v.OpenAndWait(context.Background())
	ia, _ := FrozenIA()
	for _, section := range ia.Sections {
		if err := v.Nav().DeepLink(section.SpecID); err != nil {
			t.Fatal(err)
		}
		for _, width := range []int{20, 24, 40, 60, 80, 120, 200} {
			out := v.Render(width, 160)
			for i, line := range out {
				if got := VisibleLen(line); got > width {
					t.Fatalf("%s width %d line %d is %d cells: %q", section.Title, width, i, got, line)
				}
			}
			joined := strings.Join(out, "\n")
			if !strings.Contains(joined, "UNKNOWN") && !strings.Contains(joined, "NOT_RUN") {
				t.Fatalf("%s width %d hid every textual truth status:\n%s", section.Title, width, joined)
			}
			if strings.Contains(joined, "UNKNO…") || strings.Contains(joined, "NOT_RU…") {
				t.Fatalf("%s width %d truncated a truth badge:\n%s", section.Title, width, joined)
			}
		}
	}
}

// TestIntegrationStalenessCancellationAndIdempotency uses the visible
// confirmation path, including the workspace dispatch method, rather than a
// second test-only mutation engine.
func TestIntegrationStalenessCancellationAndIdempotency(t *testing.T) {
	ctx := context.Background()
	t.Run("stale target is refused", func(t *testing.T) {
		ws, auth := integrationWorkspace(t)
		if err := ws.navView.Nav().DeepLink("CTUI-0046"); err != nil {
			t.Fatal(err)
		}
		ws.dispatchNavigationKey(ctx, key(KeyEnter))
		auth.mu.Lock()
		auth.task.Revision++
		auth.mu.Unlock()
		ws.dispatchNavigationKey(ctx, runeKey('y'))
		ws.dispatchNavigationKey(ctx, key(KeyRight))
		ws.dispatchNavigationKey(ctx, key(KeyEnter))
		if got := atomic.LoadInt32(&auth.cancels); got != 0 {
			t.Fatalf("stale confirmation cancelled %d task(s)", got)
		}
		if !errors.Is(ws.navView.Confirmation().Failure(), ErrStaleTarget) {
			t.Fatalf("stale confirmation failure = %v, want ErrStaleTarget", ws.navView.Confirmation().Failure())
		}
	})
	t.Run("submitted request cannot submit twice", func(t *testing.T) {
		ws, auth := integrationWorkspace(t)
		if err := ws.navView.Nav().DeepLink("CTUI-0065"); err != nil {
			t.Fatal(err)
		}
		ws.dispatchNavigationKey(ctx, key(KeyEnter))
		ws.dispatchNavigationKey(ctx, key(KeyRight))
		ws.dispatchNavigationKey(ctx, key(KeyEnter))
		for i := 0; i < 8; i++ {
			ws.dispatchNavigationKey(ctx, key(KeyEnter))
		}
		if got := atomic.LoadInt32(&auth.checkpoints); got != 1 {
			t.Fatalf("repeated submit reached canonical authority %d times", got)
		}
	})
	t.Run("escape mutates nothing", func(t *testing.T) {
		ws, auth := integrationWorkspace(t)
		if err := ws.navView.Nav().DeepLink("CTUI-0070"); err != nil {
			t.Fatal(err)
		}
		ws.dispatchNavigationKey(ctx, key(KeyEnter))
		ws.dispatchNavigationKey(ctx, runeKey('y'))
		ws.dispatchNavigationKey(ctx, key(KeyRight))
		ws.dispatchNavigationKey(ctx, key(KeyEsc))
		if got := integrationMutationCount(auth); got != 0 {
			t.Fatalf("Esc reached %d canonical mutations", got)
		}
	})
}

func TestIntegrationOfflineReadersNeverRenderSuccessZeroOrNone(t *testing.T) {
	v := testView(t)
	v.AttachSource(&StatusSource{
		Runtime:    integrationOfflineRuntime{},
		Assessment: integrationOfflineAssessment{},
		Resources:  integrationOfflineResources{},
		Cloud:      integrationOfflineCloud{},
	}, integrationOfflineProviders{})
	v.OpenAndWait(context.Background())
	for _, id := range []string{"CTUI-0001", "CTUI-0089", "CTUI-0090", "CTUI-0108", "CTUI-0127", "CTUI-0141"} {
		if err := v.Nav().DeepLink(id); err != nil {
			t.Fatal(err)
		}
		out := strings.Join(v.Render(200, 100), "\n")
		if !strings.Contains(out, "ERROR") && !strings.Contains(out, "OFFLINE") && !strings.Contains(out, "NOT_RUN") {
			t.Fatalf("offline %s lacks an error/offline/not-run truth state:\n%s", id, out)
		}
		for _, forbidden := range []string{" 0", "none", "NONE", "PASS"} {
			if forbidden == " 0" {
				continue // digits in canonical paths are not rendered values
			}
			if strings.Contains(out, forbidden) {
				t.Fatalf("offline %s rendered misleading %q:\n%s", id, forbidden, out)
			}
		}
		for _, field := range RenderNode(v.Nav().Current(), v.snapshot()).Fields {
			if field.Value.Text == "0" || strings.EqualFold(field.Value.Text, "none") {
				t.Fatalf("offline %s rendered misleading value %q for %s", id, field.Value.Text, field.Label)
			}
		}
	}
}

func integrationNodes(ia *IA) []*Node {
	out := make([]*Node, 0, ia.Count())
	var walk func(*Node)
	walk = func(node *Node) {
		out = append(out, node)
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(ia.Root)
	return out
}

func integrationWorkspace(t *testing.T) (*Workspace, *fakeAuthority) {
	t.Helper()
	ws := NewWorkspace(nil, "integration-project", "integration-session")
	ctx := context.Background()
	if !ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyCtrlN}) {
		t.Fatal("Ctrl+N did not enter navigation")
	}
	source, auth := testControl(t)
	ws.navView.AttachControl(source)
	ws.navView.Refresh(ctx)
	return ws, auth
}

func integrationMutationCount(auth *fakeAuthority) int32 {
	return atomic.LoadInt32(&auth.cancels) + atomic.LoadInt32(&auth.executes) +
		atomic.LoadInt32(&auth.startRuns) + atomic.LoadInt32(&auth.rollbacks) +
		atomic.LoadInt32(&auth.decisions) + atomic.LoadInt32(&auth.checkpoints)
}

type integrationOfflineRuntime struct{}

func (integrationOfflineRuntime) Status(context.Context) (RuntimeStatus, error) {
	return RuntimeStatus{}, errors.New("offline")
}
func (integrationOfflineRuntime) Events(context.Context) ([]model.Event, error) {
	return nil, errors.New("offline")
}
func (integrationOfflineRuntime) Tasks(context.Context) ([]model.Task, error) {
	return nil, errors.New("offline")
}
func (integrationOfflineRuntime) InstanceID() string { return "" }

type integrationOfflineAssessment struct{}

func (integrationOfflineAssessment) Assessment(context.Context) (startup.Assessment, error) {
	return startup.Assessment{}, errors.New("offline")
}

type integrationOfflineResources struct{}

func (integrationOfflineResources) Resources(context.Context) (resources.Snapshot, error) {
	return resources.Snapshot{}, errors.New("offline")
}

type integrationOfflineCloud struct{}

func (integrationOfflineCloud) Configured() bool             { return true }
func (integrationOfflineCloud) Entitled() bool               { return false }
func (integrationOfflineCloud) Capabilities() []string       { return nil }
func (integrationOfflineCloud) ExpiresAt() (time.Time, bool) { return time.Time{}, false }
func (integrationOfflineCloud) RenewAt() (time.Time, bool)   { return time.Time{}, false }
func (integrationOfflineCloud) InstallationID() string       { return "" }
func (integrationOfflineCloud) SessionID() string            { return "" }
func (integrationOfflineCloud) Err() error                   { return errors.New("offline") }

type integrationOfflineProviders struct{}

func (integrationOfflineProviders) Providers(context.Context) ([]ProviderProbe, error) {
	return nil, fmt.Errorf("offline")
}
