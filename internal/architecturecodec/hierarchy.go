package architecturecodec

import "strings"

// TopView is the drawing of the top-level parts; SubViewPrefix+id is the
// drawing of the parts inside component id (its sub-architecture).
const (
	TopView       = "architecture"
	SubViewPrefix = "sub:"
)

// ParentsOf maps every component id to its parent id ("" at the top level).
func ParentsOf(a map[string]any) map[string]string {
	parents := map[string]string{}
	components, _ := a["components"].([]any)
	for _, value := range components {
		c, _ := value.(map[string]any)
		id, _ := c["id"].(string)
		parent, _ := c["parent"].(string)
		parents[id] = parent
	}
	return parents
}

// ViewFor is the drawing that holds the children of parent.
func ViewFor(parent string) string {
	if parent == "" {
		return TopView
	}
	return SubViewPrefix + parent
}

// ViewParent is the component whose sub-architecture view draws, "" for the
// top level; ok is false for a view that is neither.
func ViewParent(view string) (string, bool) {
	if view == TopView {
		return "", true
	}
	if parent, found := strings.CutPrefix(view, SubViewPrefix); found && parent != "" {
		return parent, true
	}
	return "", false
}

// chainOf is id followed by its ancestors, nearest first.
func chainOf(parents map[string]string, id string) []string {
	chain := []string{id}
	seen := map[string]bool{id: true}
	for up := parents[id]; up != "" && !seen[up]; up = parents[up] {
		chain = append(chain, up)
		seen[up] = true
	}
	return chain
}

// DrawnBetween is where a connection from source to target is drawn: in the
// view of their lowest common container, between the two parts of that view
// that contain them (or are them). drawn is false when one contains the other.
func DrawnBetween(parents map[string]string, source, target string) (view, from, to string, drawn bool) {
	sourceChain, targetChain := chainOf(parents, source), chainOf(parents, target)
	inTarget := map[string]int{}
	for i, id := range targetChain {
		inTarget[id] = i
	}
	for i, id := range sourceChain {
		if j, ok := inTarget[id]; ok {
			// id contains both (or is one of them).
			if i == 0 || j == 0 {
				return "", "", "", false
			}
			return ViewFor(id), sourceChain[i-1], targetChain[j-1], true
		}
	}
	return TopView, sourceChain[len(sourceChain)-1], targetChain[len(targetChain)-1], true
}

// Contains reports whether container is id or one of its ancestors.
func Contains(parents map[string]string, container, id string) bool {
	for _, x := range chainOf(parents, id) {
		if x == container {
			return true
		}
	}
	return false
}
