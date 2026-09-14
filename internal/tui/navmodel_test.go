package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The frozen IA is a contract, so these tests assert the shape of what loads
// rather than the behaviour of the loader. A manifest that parses but produces
// the wrong tree is worse than one that fails: the menu renders and quietly
// omits places the pack declares.

func TestFrozenIALoads(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("the embedded manifest does not load: %v", err)
	}
	if ia.Root == nil {
		t.Fatal("no root")
	}
	if got := len(ia.Sections); got != 9 {
		t.Fatalf("the IA has %d top-level sections, want the immutable 9", got)
	}
}

// The manifest retains process associations for implementation traceability,
// but phase numbers are not Community navigation labels. Every visible title
// must explain the user's destination in ordinary language.
func TestCommunityNavigationUsesHumanLabels(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	for id, node := range ia.byID {
		if got := communityLabel(node.Title); strings.Contains(got, "Process 0") {
			t.Fatalf("%s leaks an implementation phase into a visible title: %q", id, got)
		}
	}
	for id, want := range map[string]string{
		"CTUI-0460": "Learning",
		"CTUI-0489": "Export learning bundle",
		"CTUI-0571": "Governed Optimization",
	} {
		node, ok := ia.Node(id)
		if !ok || communityLabel(node.Title) != want {
			got := "<missing>"
			if node != nil {
				got = communityLabel(node.Title)
			}
			t.Fatalf("%s title = %q, want %q", id, got, want)
		}
	}
}

// The nine sections and their order are immutable. Reordering them would change
// what the left-most key press selects, so the order is asserted literally.
func TestTopLevelSectionsAreFrozen(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []string{
		"Home", "Control", "Status", "Work", "Verify",
		"Memory", "Models", "Security", "System",
	}
	if len(ia.Sections) != len(want) {
		t.Fatalf("got %d sections, want %d", len(ia.Sections), len(want))
	}
	for i, section := range ia.Sections {
		if section.Title != want[i] {
			t.Fatalf("section %d is %q, want %q — the top-level order is frozen",
				i, section.Title, want[i])
		}
	}
}

// Every navigable node must be reachable from the root. A node whose parent
// chain does not terminate at the root is a screen nothing can open, which the
// loader must reject rather than render into an unreachable branch.
func TestEveryNodeIsReachable(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	seen := map[string]bool{}
	var walk func(*Node)
	walk = func(n *Node) {
		seen[n.SpecID] = true
		for _, c := range n.Children {
			if c.Parent() != n {
				t.Fatalf("%s is a child of %s but its parent points elsewhere",
					c.SpecID, n.SpecID)
			}
			walk(c)
		}
	}
	walk(ia.Root)

	for id, n := range ia.byID {
		if !n.Type.Navigable() {
			continue
		}
		if !seen[id] {
			t.Fatalf("%s (%s) is in the manifest but unreachable from the root",
				id, n.MenuPath)
		}
	}
}

// The pack declares 804 frozen IA nodes. The loader skips its own contract
// documents, so the navigable count must match that number exactly — if it
// drifts, either a place was lost or a document became a destination.
func TestNavigableCountMatchesFrozenInventory(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var file manifestFile
	if err := json.Unmarshal(frozenManifest, &file); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := ia.Count(); got != file.FrozenNodeCount {
		t.Fatalf("the IA holds %d navigable nodes, the manifest declares %d frozen nodes",
			got, file.FrozenNodeCount)
	}
}

// The 17 gaps are load-bearing: each names something the frozen TUI requires
// and no canonical binding provides. They must survive loading as gaps, because
// a gap that reads as BOUND becomes decorative UI.
func TestImplementationGapsSurviveLoading(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	gaps := ia.Gaps()
	if len(gaps) != 17 {
		t.Fatalf("the IA reports %d implementation gaps, the pack declares 17", len(gaps))
	}
	for _, g := range gaps {
		if g.Binding != BindingGap {
			t.Fatalf("%s is listed as a gap but its binding is %q", g.SpecID, g.Binding)
		}
	}
}

// Every cross-link must name an owner that exists. A cross-link pointing at
// nothing is a dead end the user cannot tell from a working one.
func TestCrossLinksResolveToRealOwners(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var checked int
	var walk func(*Node)
	walk = func(n *Node) {
		if n.Type == NodeCrossLink && n.CanonicalOwner != "" && !n.HasContextualOwner() {
			if _, ok := ia.NodeByMenuPath(n.CanonicalOwner); !ok {
				t.Fatalf("%s (%s) cross-links to %q, which is not in the IA",
					n.SpecID, n.MenuPath, n.CanonicalOwner)
			}
			checked++
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ia.Root)
	if checked == 0 {
		t.Fatal("no cross-links were checked, so this test proves nothing")
	}
}

// One cross-link resolves contextually: "Open corrective screen" leads wherever
// the selected blocker's owner is. It must be recognisable as such, or it reads
// as a dead link and would be either wrongly repaired or wrongly followed.
func TestContextualCrossLinkIsRecognised(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var contextual int
	var walk func(*Node)
	walk = func(n *Node) {
		if n.HasContextualOwner() {
			if n.Type != NodeCrossLink {
				t.Fatalf("%s has a contextual owner but is a %s, not a cross-link",
					n.SpecID, n.Type)
			}
			contextual++
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ia.Root)
	if contextual != 1 {
		t.Fatalf("expected exactly 1 contextual cross-link, found %d", contextual)
	}
}

// Safety labels are how a no-colour terminal conveys danger, so every action
// must carry one and no read-only node may.
func TestActionsCarrySafetyLabels(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n.Type.IsAction() && n.Type.SafetyLabel() == "" {
			t.Fatalf("%s is an action of type %s with no safety label", n.SpecID, n.Type)
		}
		// A non-action must never be labelled as an action. A cross-link
		// legitimately carries "[cross-link]" — that is a navigation hint, not
		// a safety class — but nothing read-only may say "action", or the row
		// would read as something the user can trigger.
		if !n.Type.IsAction() && strings.Contains(n.Type.SafetyLabel(), "action") {
			t.Fatalf("%s is a %s but carries the action label %q",
				n.SpecID, n.Type, n.Type.SafetyLabel())
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ia.Root)
}

// The palette searches destinations, so it must find a known one and must not
// invent results for nonsense.
func TestSearchFindsDestinations(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	hits := ia.Search("approvals")
	if len(hits) == 0 {
		t.Fatal("searching for a section that exists returned nothing")
	}
	// Shallower matches sort first, so the first hit should not be a leaf
	// buried under a deeper path than another match.
	for i := 1; i < len(hits); i++ {
		di := strings.Count(hits[i-1].MenuPath, "/")
		dj := strings.Count(hits[i].MenuPath, "/")
		if di > dj {
			t.Fatalf("search results are not shallowest-first: %q before %q",
				hits[i-1].MenuPath, hits[i].MenuPath)
		}
	}
	if got := ia.Search("zzzz-not-a-real-destination"); len(got) != 0 {
		t.Fatalf("search invented %d results for nonsense", len(got))
	}
	if got := ia.Search("   "); len(got) != 0 {
		t.Fatal("an empty query returned results")
	}
}

// Breadcrumbs name the canonical hierarchy, which is what makes a cross-linked
// screen honest about where it really lives.
func TestBreadcrumbFollowsCanonicalHierarchy(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Any leaf will do; pick a deep one deterministically.
	var deepest *Node
	var walk func(*Node)
	walk = func(n *Node) {
		if deepest == nil || len(n.Breadcrumb()) > len(deepest.Breadcrumb()) {
			deepest = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ia.Root)

	crumbs := deepest.Breadcrumb()
	if len(crumbs) < 2 {
		t.Fatalf("the deepest node %q has a %d-part breadcrumb", deepest.MenuPath, len(crumbs))
	}
	// The breadcrumb must be a prefix-ordered walk of the real menu path.
	joined := strings.Join(crumbs, " / ")
	if !strings.HasSuffix(deepest.MenuPath, joined) {
		t.Fatalf("breadcrumb %q does not match menu path %q", joined, deepest.MenuPath)
	}
}

// A malformed manifest must fail loudly. A loader that returns a partial tree
// renders a menu that silently omits places, and a missing node cannot be told
// from one that was never specified.
func TestLoadRejectsMalformedManifests(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"not json", `{`},
		{"no specs", `{"schema_version":1,"specs":[]}`},
		{"missing spec id", `{"specs":[{"title":"x","type":"ROOT"}]}`},
		{"unknown binding", `{"specs":[{"spec_id":"A","type":"ROOT","implementation_binding_status":"MAYBE"}]}`},
		{"unknown parent", `{"specs":[
			{"spec_id":"A","type":"ROOT","implementation_binding_status":"BOUND"},
			{"spec_id":"B","parent":"NOPE","type":"DETAIL","implementation_binding_status":"BOUND"}]}`},
		{"no root", `{"specs":[{"spec_id":"A","type":"DETAIL","parent":"","implementation_binding_status":"BOUND"}]}`},
		// A node whose ancestors form a cycle resolves its parent happily and
		// is still absent from every tree walk, so it must be rejected.
		{"disconnected cycle", `{"specs":[
			{"spec_id":"R","type":"ROOT","implementation_binding_status":"BOUND"},
			{"spec_id":"A","parent":"B","type":"DETAIL","implementation_binding_status":"BOUND"},
			{"spec_id":"B","parent":"A","type":"DETAIL","implementation_binding_status":"BOUND"}]}`},
		// A navigable node parented to a contract document renders nowhere.
		{"contract parent", `{"specs":[
			{"spec_id":"R","type":"ROOT","implementation_binding_status":"BOUND"},
			{"spec_id":"C","type":"GLOBAL_CONTRACT","implementation_binding_status":"BOUND"},
			{"spec_id":"A","parent":"C","type":"DETAIL","implementation_binding_status":"BOUND"}]}`},
		// Two nodes on one menu path make cross-link resolution ambiguous.
		{"duplicate menu path", `{"specs":[
			{"spec_id":"R","type":"ROOT","menu_path":"M","implementation_binding_status":"BOUND"},
			{"spec_id":"A","parent":"R","menu_path":"M","type":"DETAIL","implementation_binding_status":"BOUND"}]}`},
	}
	for _, c := range cases {
		if _, err := LoadIA([]byte(c.json)); err == nil {
			t.Fatalf("%s: a malformed manifest loaded without error", c.name)
		}
	}
}

// The embedded copy must match the spec pack. Two copies of the frozen IA drift
// the first time a spec moves, and nothing else would notice.
func TestEmbeddedManifestMatchesSpecPack(t *testing.T) {
	const packPath = "../../.marshal/tasks/community-tui-final/MANIFEST.json"
	pack, err := os.ReadFile(packPath)
	if err != nil {
		t.Skipf("spec pack not present in this checkout: %v", err)
	}
	// Compare parsed content rather than bytes: formatting is not the
	// contract, the declared inventory is.
	var packFile, embedded manifestFile
	if err := json.Unmarshal(pack, &packFile); err != nil {
		t.Fatalf("parse pack manifest: %v", err)
	}
	if err := json.Unmarshal(frozenManifest, &embedded); err != nil {
		t.Fatalf("parse embedded manifest: %v", err)
	}
	if len(packFile.Specs) != len(embedded.Specs) {
		t.Fatalf("the embedded manifest has %d specs, the pack has %d — they have drifted",
			len(embedded.Specs), len(packFile.Specs))
	}
	if packFile.FrozenNodeCount != embedded.FrozenNodeCount {
		t.Fatalf("frozen node count differs: embedded %d, pack %d",
			embedded.FrozenNodeCount, packFile.FrozenNodeCount)
	}
	// Every field the loader or the UI reads is compared. Comparing only ids
	// and paths would miss a node changing type, or a binding flipping from
	// IMPLEMENTATION_GAP to BOUND — the two edits most likely to make the TUI
	// claim a capability the backend does not have.
	for i := range packFile.Specs {
		p, e := packFile.Specs[i], embedded.Specs[i]
		if p.SpecID != e.SpecID {
			t.Fatalf("spec %d differs: embedded %s, pack %s", i, e.SpecID, p.SpecID)
		}
		for _, f := range []struct{ name, pack, emb string }{
			{"menu path", p.MenuPath, e.MenuPath},
			{"title", p.Title, e.Title},
			{"type", p.Type, e.Type},
			{"binding status", p.Binding, e.Binding},
			{"canonical owner", p.CanonicalOwner, e.CanonicalOwner},
			{"parent", p.Parent, e.Parent},
		} {
			if f.pack != f.emb {
				t.Fatalf("%s %s differs between embedded copy and pack: embedded %q, pack %q",
					p.SpecID, f.name, f.emb, f.pack)
			}
		}
	}
}

// The frozen top level is enforced by the loader, not only asserted by a test.
// A manifest that renamed or reordered a section must fail to load.
func TestLoaderRejectsRenamedOrReorderedSections(t *testing.T) {
	pack, err := os.ReadFile("../../.marshal/tasks/community-tui-final/MANIFEST.json")
	if err != nil {
		t.Skipf("spec pack not present: %v", err)
	}
	var file manifestFile
	if err := json.Unmarshal(pack, &file); err != nil {
		t.Fatalf("parse: %v", err)
	}

	renamed := file
	renamed.Specs = append([]manifestSpec(nil), file.Specs...)
	found := false
	for i := range renamed.Specs {
		if renamed.Specs[i].Type == string(NodeSection) && renamed.Specs[i].Title == "Verify" {
			renamed.Specs[i].Title = "Validation"
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no Verify section to rename, so this test proves nothing")
	}
	raw, err := json.Marshal(renamed)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	if _, err := LoadIA(raw); err == nil {
		t.Fatal("a manifest that renamed a top-level section loaded without error")
	}
}

// A cross-link's breadcrumb must name the canonical hierarchy of the node it
// points at, not the menu the link was clicked from.
func TestCrossLinkTargetBreadcrumbIsCanonical(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	checked := 0
	var walk func(*Node)
	walk = func(n *Node) {
		if n.Type == NodeCrossLink && !n.HasContextualOwner() {
			target, ok := ia.NodeByMenuPath(n.CanonicalOwner)
			if !ok {
				t.Fatalf("%s points at %q, which is not in the IA", n.SpecID, n.CanonicalOwner)
			}
			// The breadcrumb omits the root, which is the application itself,
			// so the canonical menu path is the breadcrumb under that root.
			crumb := PathString(target)
			want := strings.TrimPrefix(target.MenuPath, ia.Root.Title+" / ")
			if crumb != want {
				t.Fatalf("%s's target breadcrumb is %q but its canonical path is %q",
					n.SpecID, crumb, want)
			}
			if n != target && PathString(n) == crumb {
				t.Fatalf("%s's breadcrumb is indistinguishable from its target", n.SpecID)
			}
			checked++
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ia.Root)
	if checked == 0 {
		t.Fatal("no resolvable cross-links were checked, so this test proves nothing")
	}
	t.Logf("checked %d resolvable cross-links", checked)
}
