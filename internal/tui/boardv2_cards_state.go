package tui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The decision and paused cards are drawn on every frame, so nothing they
// show is read on the paint path: the waiting contract's sketch and the
// commit counts of paused branches are read once in the background and kept
// here, keyed by what they describe, and a frame shows what is known.

// v2CardState is one workspace's card cache and the card keys' own state.
type v2CardState struct {
	mu sync.Mutex
	// sketchKey is the gate key the sketch was read for; sketch is nil
	// while it is read or when no contract could be read.
	sketchKey     string
	sketch        *decisionSketch
	sketchLoading string
	// commits is "base..tip" -> commits on the branch; a key under
	// commitsLoading is being counted.
	commits        map[string]int
	commitsLoading map[string]bool
	// dismissed is the decision the owner put off with Esc ("later"); it
	// shows again once a different decision waits.
	dismissed string
	// refusedSeen is when the Board first saw the Marshal's own accept of
	// a gate refused, for a refusal whose Activity line is gone.
	refusedSeen map[string]time.Time
	// planArmed is the plan decision whose Enter waits for a second Enter.
	planArmed string
	// shown is the v2 screen the last Board paint drew; "" when the
	// classic Board or a view drew it (boardv2_paint.go).
	shown vm.Screen
}

// v2Cards holds every workspace's card state. A workspace is one per
// process outside tests, so the map stays tiny.
var v2Cards sync.Map // *Workspace -> *v2CardState

func (w *Workspace) v2CardState() *v2CardState {
	if s, ok := v2Cards.Load(w); ok {
		return s.(*v2CardState)
	}
	s, _ := v2Cards.LoadOrStore(w, &v2CardState{})
	return s.(*v2CardState)
}

// decisionSketch is the waiting project contract as the decision card
// draws it.
type decisionSketch struct {
	Parts, Links, Changed int
	Covers                []string
	Nodes                 []vm.Node
}

// sketchContract draws pending against the accepted contract it revises
// (zero for a first draft) with the same comparison the browser review
// uses (projectcontext.ContractChanges): counts, what it covers, and its
// top-level parts with the ones this goal adds, changes or removes marked.
func sketchContract(pending, accepted projectcontext.Contract) decisionSketch {
	changes := projectcontext.ContractChanges(accepted, pending)
	var s decisionSketch
	covers := map[string]bool{}
	for _, c := range changes.Components {
		changed := c.Change != projectcontext.ChangeExisting
		if c.Change != projectcontext.ChangeRemoved {
			s.Parts++
			for _, r := range c.Covers {
				if r = strings.TrimSpace(r); r != "" {
					covers[r] = true
				}
			}
		}
		if changed {
			s.Changed++
		}
		if c.Parent == "" {
			s.Nodes = append(s.Nodes, vm.Node{Name: c.ID, Sub: firstLine(c.Responsibility), Changed: changed})
		}
	}
	for _, e := range changes.Connections {
		if e.Change != projectcontext.ChangeRemoved {
			s.Links++
		}
	}
	for r := range covers {
		s.Covers = append(s.Covers, r)
	}
	sortRequirements(s.Covers)
	// Unchanged parts first, as the sketch reads left to right from what
	// exists to what this goal brings.
	slices.SortStableFunc(s.Nodes, func(a, b vm.Node) int {
		if a.Changed != b.Changed {
			if a.Changed {
				return 1
			}
			return -1
		}
		return 0
	})
	return s
}

// sortRequirements orders ids naturally: R2 before R10.
func sortRequirements(ids []string) {
	sort.SliceStable(ids, func(i, j int) bool {
		pi, ni := requirementParts(ids[i])
		pj, nj := requirementParts(ids[j])
		if pi != pj {
			return pi < pj
		}
		if ni != nj {
			return ni < nj
		}
		return ids[i] < ids[j]
	})
}

// requirementParts splits "R12" into "R" and 12; an id without a trailing
// number has -1.
func requirementParts(id string) (string, int) {
	i := len(id)
	for i > 0 && id[i-1] >= '0' && id[i-1] <= '9' {
		i--
	}
	n, err := strconv.Atoi(id[i:])
	if err != nil {
		return id, -1
	}
	return id[:i], n
}

// coversText is what the requirements a contract covers read as:
// "R1–R18" for one unbroken numbered range, "R1, R3, R7" for a few, and a
// count otherwise.
func coversText(ids []string) string {
	switch {
	case len(ids) == 0:
		return ""
	case len(ids) == 1:
		return ids[0]
	}
	prefix, first := requirementParts(ids[0])
	run := first >= 0
	for i, id := range ids {
		p, n := requirementParts(id)
		if p != prefix || n != first+i {
			run = false
			break
		}
	}
	if run {
		return ids[0] + "–" + ids[len(ids)-1]
	}
	if len(ids) <= 4 {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%d requirements", len(ids))
}

// cachedSketch is the sketch read for gate key, or nil; a sketch for
// another key starts a read in the background.
func (w *Workspace) cachedSketch(key string) *decisionSketch {
	s := w.v2CardState()
	s.mu.Lock()
	if s.sketchKey == key {
		sk := s.sketch
		s.mu.Unlock()
		return sk
	}
	if s.sketchLoading == key {
		s.mu.Unlock()
		return nil
	}
	s.sketchLoading = key
	s.mu.Unlock()
	read := v2ContractSource
	if !w.startBackground(w.workers.context(), func(ctx context.Context) { w.loadSketch(ctx, key, read) }) {
		s.mu.Lock()
		s.sketchLoading = ""
		s.mu.Unlock()
	}
	return nil
}

// v2ContractSource is a test's seam for the waiting and accepted contracts;
// nil reads the store.
var v2ContractSource func(w *Workspace, ctx context.Context) (pending, accepted projectcontext.Contract, found bool, err error)

// loadSketch reads the waiting contract once (through read, nil for the
// store) and keeps its sketch for key.
func (w *Workspace) loadSketch(ctx context.Context, key string, read func(*Workspace, context.Context) (projectcontext.Contract, projectcontext.Contract, bool, error)) {
	if read == nil {
		read = func(w *Workspace, ctx context.Context) (projectcontext.Contract, projectcontext.Contract, bool, error) {
			return w.readDecisionContracts(ctx)
		}
	}
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pending, accepted, found, err := read(w, readCtx)
	var sk *decisionSketch
	if err == nil && found {
		v := sketchContract(pending, accepted)
		sk = &v
	}
	s := w.v2CardState()
	s.mu.Lock()
	if s.sketchLoading == key {
		s.sketchLoading = ""
	}
	// A failed read keeps no sketch: the card shows the gate's own summary
	// until the waiting contract changes and is read again.
	s.sketchKey, s.sketch = key, sk
	s.mu.Unlock()
	if sk != nil {
		w.requestRepaint()
	}
}

// v2CommitCount is the seam that counts the commits on a branch between
// base and tip; nil asks Git (read only).
var v2CommitCount func(ctx context.Context, root, base, tip string) (int, error)

// countBranchCommits is git rev-list --count base..tip in root.
func countBranchCommits(ctx context.Context, root, base, tip string) (int, error) {
	out, err := scanGitOutput(ctx, root, "rev-list", "--count", base+".."+tip)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// cachedCommits is the number of commits base..tip, and whether it is
// known; an unknown count is counted in the background.
func (w *Workspace) cachedCommits(base, tip string) (int, bool) {
	if base == "" || tip == "" {
		return 0, false
	}
	key := base + ".." + tip
	s := w.v2CardState()
	s.mu.Lock()
	if n, ok := s.commits[key]; ok {
		// A count Git could not give is -1: unknown, and not asked again.
		s.mu.Unlock()
		return n, n >= 0
	}
	if s.commitsLoading[key] {
		s.mu.Unlock()
		return 0, false
	}
	root := ""
	w.mu.RLock()
	if w.runtime != nil {
		root = w.runtime.ProjectRoot()
	}
	w.mu.RUnlock()
	if root == "" && v2CommitCount == nil {
		s.mu.Unlock()
		return 0, false
	}
	if s.commitsLoading == nil {
		s.commitsLoading = map[string]bool{}
	}
	s.commitsLoading[key] = true
	s.mu.Unlock()
	count := v2CommitCount
	if count == nil {
		count = countBranchCommits
	}
	started := w.startBackground(w.workers.context(), func(ctx context.Context) {
		countCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		n, err := count(countCtx, root, base, tip)
		s.mu.Lock()
		delete(s.commitsLoading, key)
		if err != nil {
			n = -1
		}
		if s.commits == nil {
			s.commits = map[string]int{}
		}
		s.commits[key] = n
		s.mu.Unlock()
		if err == nil {
			w.requestRepaint()
		}
	})
	if !started {
		s.mu.Lock()
		delete(s.commitsLoading, key)
		s.mu.Unlock()
	}
	return 0, false
}
