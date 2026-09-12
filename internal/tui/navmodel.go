package tui

// The frozen Community TUI information architecture, loaded from the spec
// pack's MANIFEST.json rather than restated in Go.
//
// The manifest is the source of truth for what exists and where it lives. A
// hand-written tree here would be a second copy of the frozen IA, and the two
// would drift the first time a spec moved — with nothing to catch it. Loading
// the manifest means a node the pack does not declare cannot appear on screen,
// and a node it does declare cannot be silently dropped.
//
// This file is navigation and presentation only. It creates no backend
// capability, holds no authority, and knows nothing about how a screen fetches
// its data; that binding lives with each screen.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// NodeType classifies a manifest entry.
//
// The distinction that matters at runtime is between reading and mutating: a
// DETAIL never offers an action control, and the four action types each carry a
// safety class the action bar must display.
type NodeType string

const (
	NodeRoot        NodeType = "ROOT"
	NodeSection     NodeType = "TOP_LEVEL_SECTION"
	NodeSubmenu     NodeType = "SUBMENU"
	NodeDetail      NodeType = "DETAIL"
	NodeCrossLink   NodeType = "CROSS_LINK"
	NodeAction      NodeType = "ACTION"
	NodeGoverned    NodeType = "GOVERNED_ACTION"
	NodeSensitive   NodeType = "SENSITIVE_ACTION"
	NodeDestructive NodeType = "DESTRUCTIVE_ACTION"

	// The pack also carries its own contracts as entries. They are documents
	// about the TUI rather than places inside it, so navigation skips them.
	NodeGlobalContract NodeType = "GLOBAL_CONTRACT"
	NodeImplSequence   NodeType = "IMPLEMENTATION_SEQUENCE"
	NodeAcceptance     NodeType = "ACCEPTANCE_CONTRACT"
	NodeTraceability   NodeType = "TRACEABILITY"
)

// IsAction reports whether selecting this node mutates canonical state.
func (t NodeType) IsAction() bool {
	switch t {
	case NodeAction, NodeGoverned, NodeSensitive, NodeDestructive:
		return true
	}
	return false
}

// SafetyLabel is the text the action bar must show, per the UX contract. It is
// returned as text rather than colour because colour is supplementary: a
// no-colour terminal must still convey the safety class.
func (t NodeType) SafetyLabel() string {
	switch t {
	case NodeAction:
		return "[action]"
	case NodeGoverned:
		return "[governed action]"
	case NodeSensitive:
		return "[sensitive action]"
	case NodeDestructive:
		return "[destructive action]"
	case NodeCrossLink:
		return "[cross-link]"
	}
	return ""
}

// Navigable reports whether the node is a place a user can stand.
//
// The pack's own contract documents are entries in the manifest but are not
// destinations, and including them would put "UX Contract" in the Home menu.
func (t NodeType) Navigable() bool {
	switch t {
	case NodeGlobalContract, NodeImplSequence, NodeAcceptance, NodeTraceability:
		return false
	}
	return true
}

// BindingStatus records whether a node has a real canonical binding.
type BindingStatus string

const (
	// BindingBound means the spec names a backend source that exists.
	BindingBound BindingStatus = "BOUND"
	// BindingGap means the frozen TUI requires something no canonical
	// binding provides yet. Such a node renders, and its action stays
	// disabled with the reason shown, rather than being hidden — hiding it
	// would make the gap invisible and the IA incomplete.
	BindingGap BindingStatus = "IMPLEMENTATION_GAP"
)

// Node is one place in the frozen IA.
type Node struct {
	SpecID   string
	Title    string
	SpecPath string
	MenuPath string
	ParentID string
	Type     NodeType
	Binding  BindingStatus
	// CanonicalOwner is set on cross-links: the one node that actually owns
	// the mutation this one points at. A cross-link navigates there and
	// inherits none of its authority.
	//
	// One cross-link resolves at runtime instead: "Open corrective screen"
	// leads wherever the selected blocker's owner is, which is not knowable
	// until a blocker is selected. Such an owner carries the CONTEXTUAL:
	// prefix and must be resolved from the selected record, never guessed.
	CanonicalOwner string
	// BackendSources are the files the spec names as the canonical binding.
	// They are carried for traceability, not dereferenced at runtime.
	BackendSources []string
	Process        string

	// Children are resolved from ParentID at load, in manifest order, which
	// is the frozen order the pack declares.
	Children []*Node
	parent   *Node
}

// Parent returns the enclosing node, or nil at the root.
func (n *Node) Parent() *Node { return n.parent }

// IsLeaf reports whether the node has no navigable children.
func (n *Node) IsLeaf() bool { return len(n.Children) == 0 }

// Breadcrumb returns the titles from the root down to this node.
//
// The navigation contract requires the breadcrumb to name the canonical
// hierarchy even when the user arrived by a cross-link, so it is computed from
// the tree rather than from how the user got here.
func (n *Node) Breadcrumb() []string {
	var parts []string
	for cur := n; cur != nil; cur = cur.parent {
		if cur.Type == NodeRoot {
			break
		}
		parts = append(parts, cur.Title)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return parts
}

// IA is the loaded frozen information architecture.
type IA struct {
	Root     *Node
	Sections []*Node
	byID     map[string]*Node
	byPath   map[string]*Node
}

// manifestSpec mirrors one MANIFEST.json entry.
type manifestSpec struct {
	SpecID         string   `json:"spec_id"`
	Title          string   `json:"title"`
	Path           string   `json:"path"`
	MenuPath       string   `json:"menu_path"`
	Parent         string   `json:"parent"`
	CanonicalOwner string   `json:"canonical_owner"`
	Type           string   `json:"type"`
	Process        string   `json:"process_association"`
	Binding        string   `json:"implementation_binding_status"`
	FrozenIANode   bool     `json:"frozen_ia_node"`
	BackendSources []string `json:"backend_sources"`
}

type manifestFile struct {
	SchemaVersion   int            `json:"schema_version"`
	Pack            string         `json:"pack"`
	FrozenRoot      string         `json:"frozen_root"`
	FrozenNodeCount int            `json:"frozen_ia_node_count"`
	Specs           []manifestSpec `json:"specs"`
}

// LoadIA parses a MANIFEST.json into the navigable tree.
//
// It fails rather than degrades on a malformed manifest. A partially loaded IA
// would render a menu that silently omits nodes, and a missing node is
// indistinguishable from a node that was never specified — which is exactly the
// confusion the frozen pack exists to prevent.
func LoadIA(raw []byte) (*IA, error) {
	var file manifestFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("tui: parse manifest: %w", err)
	}
	if len(file.Specs) == 0 {
		return nil, fmt.Errorf("tui: manifest declares no specs")
	}

	ia := &IA{
		byID:   make(map[string]*Node, len(file.Specs)),
		byPath: make(map[string]*Node, len(file.Specs)),
	}

	// First pass: every entry becomes a node, so a parent reference can
	// resolve regardless of the order entries appear in.
	order := make([]string, 0, len(file.Specs))
	for _, s := range file.Specs {
		if s.SpecID == "" {
			return nil, fmt.Errorf("tui: manifest entry %q has no spec id", s.Title)
		}
		if _, seen := ia.byID[s.SpecID]; seen {
			return nil, fmt.Errorf("tui: duplicate spec id %s", s.SpecID)
		}
		binding := BindingStatus(s.Binding)
		if binding != BindingBound && binding != BindingGap {
			return nil, fmt.Errorf("tui: %s has unknown binding status %q", s.SpecID, s.Binding)
		}
		n := &Node{
			SpecID:         s.SpecID,
			Title:          s.Title,
			SpecPath:       s.Path,
			MenuPath:       s.MenuPath,
			ParentID:       s.Parent,
			Type:           NodeType(s.Type),
			Binding:        binding,
			CanonicalOwner: s.CanonicalOwner,
			BackendSources: s.BackendSources,
			Process:        s.Process,
		}
		ia.byID[s.SpecID] = n
		if s.MenuPath != "" {
			// A colliding menu path would silently displace the earlier node
			// from NodeByMenuPath, and cross-links resolve by menu path — so a
			// collision breaks navigation to a node that still renders.
			if prior, clash := ia.byPath[s.MenuPath]; clash {
				return nil, fmt.Errorf(
					"tui: %s and %s share the menu path %q",
					prior.SpecID, s.SpecID, s.MenuPath)
			}
			ia.byPath[s.MenuPath] = n
		}
		order = append(order, s.SpecID)
	}

	// Second pass: link children in manifest order, which is the frozen order.
	for _, id := range order {
		n := ia.byID[id]
		if !n.Type.Navigable() {
			continue
		}
		if n.Type == NodeRoot {
			if ia.Root != nil {
				return nil, fmt.Errorf("tui: manifest declares more than one root")
			}
			ia.Root = n
			continue
		}
		parent, ok := ia.byID[n.ParentID]
		if !ok {
			return nil, fmt.Errorf("tui: %s names unknown parent %q", n.SpecID, n.ParentID)
		}
		// A navigable node parented to a contract document would be attached to
		// a node that is never itself linked into the tree, so it would render
		// nowhere while still counting as loaded.
		if !parent.Type.Navigable() {
			return nil, fmt.Errorf(
				"tui: %s is parented to %s, which is not a navigable node",
				n.SpecID, parent.SpecID)
		}
		n.parent = parent
		parent.Children = append(parent.Children, n)
	}

	if ia.Root == nil {
		return nil, fmt.Errorf("tui: manifest declares no root")
	}
	for _, child := range ia.Root.Children {
		if child.Type == NodeSection {
			ia.Sections = append(ia.Sections, child)
		}
	}
	if len(ia.Sections) != 9 {
		// The nine sections are immutable. A manifest that produces a
		// different number is not a manifest this build can render.
		return nil, fmt.Errorf("tui: expected 9 top-level sections, manifest has %d", len(ia.Sections))
	}
	// The names and their order are as frozen as the count. Checking only the
	// count would accept a manifest that renamed or reordered the top level.
	for i, want := range FrozenSections {
		if got := ia.Sections[i].Title; got != want {
			return nil, fmt.Errorf(
				"tui: top-level section %d is %q, but the frozen IA declares %q",
				i, got, want)
		}
	}

	// Every navigable node must hang off the root. A node whose ancestors form
	// a cycle, or that is otherwise disconnected, resolves its parent happily
	// in the pass above and is still absent from every tree walk — it would
	// vanish from the menu without producing an error anywhere.
	reached := 0
	var walk func(*Node)
	walk = func(n *Node) {
		reached++
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ia.Root)

	navigable := 0
	for _, n := range ia.byID {
		if n.Type.Navigable() {
			navigable++
		}
	}
	if reached != navigable {
		return nil, fmt.Errorf(
			"tui: %d navigable nodes exist but only %d are reachable from the root",
			navigable, reached)
	}
	// The manifest states how many frozen nodes it carries. Rendering fewer
	// than it declares is the failure this whole loader exists to prevent, so
	// the count is checked rather than assumed.
	if file.FrozenNodeCount != 0 && reached != file.FrozenNodeCount {
		return nil, fmt.Errorf(
			"tui: manifest declares %d frozen nodes but %d are reachable",
			file.FrozenNodeCount, reached)
	}
	return ia, nil
}

// FrozenSections is the immutable top-level menu, in its frozen order.
//
// It is duplicated from the manifest deliberately: it is the one part of the IA
// that must not change silently, so a manifest that disagrees with it fails to
// load rather than quietly redrawing the top navigation.
var FrozenSections = [9]string{
	"Home", "Control", "Status", "Work", "Verify",
	"Memory", "Models", "Security", "System",
}

// Node returns a node by spec id.
func (ia *IA) Node(specID string) (*Node, bool) {
	n, ok := ia.byID[specID]
	return n, ok
}

// ContextualOwnerPrefix marks a cross-link whose target depends on the record
// the user selected rather than on a fixed place in the IA.
const ContextualOwnerPrefix = "CONTEXTUAL:"

// HasContextualOwner reports whether this cross-link resolves at runtime.
//
// Such a link cannot be validated against the tree, and must not be followed
// until a selection supplies the target. Treating it as broken would be wrong;
// treating it as fixed would send the user to the wrong screen.
func (n *Node) HasContextualOwner() bool {
	return strings.HasPrefix(n.CanonicalOwner, ContextualOwnerPrefix)
}

// NodeByMenuPath resolves a full canonical menu path.
//
// Cross-links name their owner by path, so this is how a cross-link is followed
// without the target having to be addressable by spec id.
func (ia *IA) NodeByMenuPath(path string) (*Node, bool) {
	n, ok := ia.byPath[path]
	return n, ok
}

// Count reports how many navigable nodes the IA holds.
func (ia *IA) Count() int {
	total := 0
	var walk func(*Node)
	walk = func(n *Node) {
		total++
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ia.Root)
	return total
}

// Gaps returns every node whose binding is still an implementation gap,
// ordered by spec id so the list is stable between runs.
func (ia *IA) Gaps() []*Node {
	var out []*Node
	var walk func(*Node)
	walk = func(n *Node) {
		if n.Binding == BindingGap {
			out = append(out, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ia.Root)
	sort.Slice(out, func(i, j int) bool { return out[i].SpecID < out[j].SpecID })
	return out
}

// Search returns navigable nodes whose title or menu path matches every
// whitespace-separated term, case-insensitively.
//
// This backs the command palette. It searches destinations the manifest
// declares and nothing else, so the palette cannot reach a screen that is not
// part of the frozen IA — and it returns nodes rather than actions, because the
// palette navigates and never bypasses a disabled state or a confirmation.
func (ia *IA) Search(query string) []*Node {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil
	}
	var out []*Node
	var walk func(*Node)
	walk = func(n *Node) {
		if n.Type != NodeRoot {
			haystack := strings.ToLower(n.Title + " " + n.MenuPath)
			matched := true
			for _, t := range terms {
				if !strings.Contains(haystack, t) {
					matched = false
					break
				}
			}
			if matched {
				out = append(out, n)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ia.Root)

	// Shallower matches first: a section is a more useful answer than a leaf
	// buried under it, and a user typing two letters wants the section.
	sort.SliceStable(out, func(i, j int) bool {
		di := strings.Count(out[i].MenuPath, "/")
		dj := strings.Count(out[j].MenuPath, "/")
		if di != dj {
			return di < dj
		}
		return out[i].SpecID < out[j].SpecID
	})
	return out
}
