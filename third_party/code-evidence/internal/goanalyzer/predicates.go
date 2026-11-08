package goanalyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// Relations.
const (
	RelDeclaredIn = "declared_in"
	RelCallers    = "callers"
	RelCallees    = "callees"
	RelReferences = "references"
	RelTestsOf    = "tests_of"
)

// Methods.
const (
	// MethodScope: syntactic, resolved by package scope and imports.
	MethodScope = "syntactic/scope"
	// MethodNameMatch: syntactic, member calls matched by name only.
	MethodNameMatch = "syntactic/name-match"
	// MethodDecl: the declaration itself.
	MethodDecl = "syntactic/decl"
)

// Bounds (approximation direction of the delivered set relative to the
// true relation).
const (
	BoundExact = "exact" // the set is the relation
	BoundLower = "lower" // every item is in the relation; more may exist
	BoundNone  = "none"  // neither: may include false positives and miss items
)

// Item is one element of a predicate result. Key is the identity used for
// validation; it is stable under edits that only shift lines.
type Item struct {
	Key     string   `json:"key"`
	Path    string   `json:"path"`
	Line    int      `json:"line"`
	Symbol  string   `json:"symbol,omitempty"`
	Configs []string `json:"configs,omitempty"`
}

// Result is an evaluated predicate.
type Result struct {
	Relation string   `json:"relation"`
	Subject  string   `json:"subject"`
	Method   string   `json:"method"`
	Bound    string   `json:"bound"`
	Items    []Item   `json:"items"`
	Coverage Coverage `json:"coverage"`
	// Deps is a digest of inputs whose change may rebind the result
	// without changing any occurrence (e.g. methods sharing the name).
	Deps string `json:"deps,omitempty"`
	// Unsupported is set when the relation does not apply to the subject.
	Unsupported bool `json:"unsupported,omitempty"`
}

// Digest is a stable hash of the item keys.
func (r *Result) Digest() string { return DigestItems(r.Items) }

// DigestItems hashes item keys (order-independent).
func DigestItems(items []Item) string {
	keys := make([]string, len(items))
	for i, it := range items {
		keys[i] = it.Key
	}
	sort.Strings(keys)
	h := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return hex.EncodeToString(h[:16])
}

// SubjectKind returns the kind of the decls with id ("" if absent).
func (ix *Index) SubjectKind(id string) string {
	refs := ix.declByID[id]
	if len(refs) == 0 {
		return ""
	}
	_, d := ix.Decl(refs[0])
	return d.Kind
}

// Evaluate computes relation rel for subject id. ok is false when the
// subject does not exist in the visible snapshot.
func (ix *Index) Evaluate(rel, id string) (Result, bool) {
	refs := ix.declByID[id]
	if len(refs) == 0 {
		return Result{}, false
	}
	_, first := ix.Decl(refs[0])
	r := Result{Relation: rel, Subject: id, Coverage: ix.Coverage, Items: []Item{}}
	switch rel {
	case RelDeclaredIn:
		// Exact only over a complete universe: a Go file that failed to
		// parse, or was not captured, may declare the same id.
		r.Method, r.Bound = MethodDecl, BoundExact
		if !ix.Coverage.Complete {
			r.Bound = BoundLower
		}
		for _, dr := range refs {
			ff, d := ix.Decl(dr)
			r.Items = append(r.Items, Item{Key: ff.Path + "#" + d.ID, Path: ff.Path, Line: d.StartLine, Symbol: d.ID, Configs: ix.ConfigNames(dr.File)})
		}
	case RelCallers:
		switch first.Kind {
		case KindFunc:
			r.Method, r.Bound = MethodScope, BoundLower
			r.Items = ix.sites(ix.refsTo[id], true, id)
		case KindMethod:
			r.Method, r.Bound = MethodNameMatch, BoundNone
			r.Items = ix.sites(ix.memberUses[first.Name], true, id)
			r.Deps = ix.methodsDigest(first.Name)
		default:
			r.Method, r.Bound, r.Unsupported = MethodScope, BoundNone, true
		}
	case RelReferences:
		if first.Kind == KindMethod {
			r.Method, r.Bound, r.Unsupported = MethodNameMatch, BoundNone, true
			break
		}
		r.Method, r.Bound = MethodScope, BoundLower
		r.Items = ix.sites(ix.refsTo[id], false, id)
	case RelCallees:
		if first.Kind != KindFunc && first.Kind != KindMethod {
			r.Method, r.Bound, r.Unsupported = MethodScope, BoundNone, true
			break
		}
		r.Method, r.Bound = MethodScope, BoundLower
		r.Items = ix.callees(refs)
	case RelTestsOf:
		var trs []DeclRef
		if first.Kind == KindMethod {
			r.Method, r.Bound = MethodNameMatch, BoundNone
			trs = ix.testsName[first.Name]
			r.Deps = ix.methodsDigest(first.Name)
		} else {
			r.Method, r.Bound = MethodScope, BoundLower
			trs = ix.testsTo[id]
		}
		for _, tr := range trs {
			ff, d := ix.Decl(tr)
			r.Items = append(r.Items, Item{Key: ff.Path + "#" + d.ID, Path: ff.Path, Line: d.StartLine, Symbol: d.ID, Configs: ix.ConfigNames(tr.File)})
		}
	default:
		r.Unsupported = true
	}
	sortItems(r.Items)
	return r, true
}

func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Path != items[j].Path {
			return items[i].Path < items[j].Path
		}
		if items[i].Line != items[j].Line {
			return items[i].Line < items[j].Line
		}
		return items[i].Key < items[j].Key
	})
}

// sites converts uses to items keyed by (path, enclosing, ordinal).
func (ix *Index) sites(uses []UseRef, callsOnly bool, subject string) []Item {
	items := []Item{}
	ord := map[string]int{}
	for _, ur := range uses {
		ff, u := ix.Use(ur)
		if callsOnly && !u.Call {
			continue
		}
		if u.Enclosing == subject && !callsOnly {
			continue // self-reference inside the declaration
		}
		k := ff.Path + "#" + u.Enclosing
		ord[k]++
		items = append(items, Item{
			Key:     k + "#" + strconv.Itoa(ord[k]),
			Path:    ff.Path,
			Line:    u.Line,
			Symbol:  u.Enclosing,
			Configs: ix.ConfigNames(ur.File),
		})
	}
	return items
}

func (ix *Index) callees(refs []DeclRef) []Item {
	items := []Item{}
	seen := map[string]bool{}
	for _, dr := range refs {
		ff, d := ix.Decl(dr)
		for _, ur := range ix.UsesIn(ff.Path, d.ID) {
			_, u := ix.Use(ur)
			if !u.Call {
				continue
			}
			var targets []string
			switch u.Kind {
			case UseMember:
				targets = []string{"?." + u.Name}
			case UsePkg:
				ids := ix.resolveIDs(ur.File, u)
				if len(ids) == 0 {
					targets = []string{"ext:" + u.Qual + "." + u.Name}
				} else {
					targets = ids
				}
			case UseLocal:
				targets = ix.resolveIDs(ur.File, u)
			}
			for _, t := range targets {
				if seen[t] {
					continue
				}
				seen[t] = true
				items = append(items, Item{Key: t, Path: ff.Path, Line: u.Line, Symbol: t})
			}
		}
	}
	return items
}

func (ix *Index) methodsDigest(name string) string {
	var keys []string
	for _, r := range ix.methods[name] {
		ff, d := ix.Decl(r)
		keys = append(keys, ff.Path+"#"+d.ID)
	}
	sort.Strings(keys)
	h := sha256.Sum256([]byte(name + "\n" + strings.Join(keys, "\n")))
	return hex.EncodeToString(h[:16])
}

// MethodDecls lists method decl locations named name as items (used for
// rebinding anchors).
func (ix *Index) MethodDecls(name string) []Item {
	var items []Item
	for _, r := range ix.methods[name] {
		ff, d := ix.Decl(r)
		items = append(items, Item{Key: ff.Path + "#" + d.ID, Path: ff.Path, Line: d.StartLine, Symbol: d.ID})
	}
	sortItems(items)
	return items
}

// Neighbors returns scope-resolved call neighbours of id (callees then
// callers), used for one bounded graph hop in ranking.
func (ix *Index) Neighbors(id string, limit int) []string {
	return ix.NeighborsExcept(id, limit, nil)
}

// NeighborsExcept is Neighbors with the files for which skip reports true
// removed from the graph: their declarations contribute no callees, their
// uses no callers, and a neighbour declared only in them is not returned
// (so it cannot take one of the limit slots either). skip == nil skips
// nothing.
func (ix *Index) NeighborsExcept(id string, limit int, skip func(path string) bool) []string {
	skipped := func(fi int) bool { return skip != nil && skip(ix.Files[fi].Path) }
	declared := func(s string) bool {
		for _, r := range ix.declByID[s] {
			if !skipped(r.File) {
				return true
			}
		}
		return false
	}
	var out []string
	seen := map[string]bool{id: true}
	add := func(s string) {
		if len(out) < limit && !seen[s] && declared(s) {
			seen[s] = true
			out = append(out, s)
		}
	}
	var own []DeclRef
	for _, r := range ix.declByID[id] {
		if !skipped(r.File) {
			own = append(own, r)
		}
	}
	for _, it := range ix.callees(own) {
		add(it.Symbol)
	}
	for _, ur := range ix.refsTo[id] {
		if skipped(ur.File) {
			continue
		}
		_, u := ix.Use(ur)
		if u.Call && u.Enclosing != "" {
			add(u.Enclosing)
		}
	}
	return out
}
