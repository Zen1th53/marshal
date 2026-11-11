package layout

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func describe(l Layout) string {
	var b []string
	for _, p := range l.Panes {
		b = append(b, fmt.Sprintf("%s:%s %d,%d %dx%d", p.ID, p.Density, p.Rect.X, p.Rect.Y, p.Rect.W, p.Rect.H))
	}
	for _, r := range l.Rules {
		b = append(b, fmt.Sprintf("rule %d", r.X))
	}
	return strings.Join(b, " | ")
}

func overlaps(r, o Rect) bool {
	return r.X < o.X+o.W && o.X < r.X+r.W && r.Y < o.Y+o.H && o.Y < r.Y+r.H
}

func inside(r, o Rect) bool {
	return r.X >= o.X && r.Y >= o.Y && r.X+r.W <= o.X+o.W && r.Y+r.H <= o.Y+o.H
}

func TestComputeAtEveryBreakpointAndMode(t *testing.T) {
	for _, tc := range []struct {
		cols, rows int
		mode       Mode
		want       string
	}{
		{79, 24, Home, "vitals:strip 0,0 79x1 | notice:strip 0,1 79x1 | inbox:compact 0,2 79x22"},
		{80, 23, Home, "vitals:strip 0,0 80x1 | notice:strip 0,1 80x1 | inbox:compact 0,2 80x21"},
		{80, 24, Home, "vitals:compact 0,0 80x1 | header:compact 0,1 80x1 | footer:compact 0,23 80x1 | inbox:full 0,2 80x5 | tasks:brief 0,8 80x7 | lanes:strip 0,16 80x1 | activity:compact 0,18 80x2"},
		{100, 30, Home, "vitals:compact 0,0 100x1 | header:compact 0,1 100x1 | footer:compact 0,29 100x1 | inbox:full 0,2 100x5 | tasks:brief 0,8 100x7 | lanes:strip 0,16 100x1 | activity:compact 0,18 100x3"},
		{109, 30, Home, "vitals:compact 0,0 109x1 | header:compact 0,1 109x1 | footer:compact 0,29 109x1 | inbox:full 0,2 109x5 | tasks:brief 0,8 109x7 | lanes:strip 0,16 109x1 | activity:compact 0,18 109x3"},
		{110, 30, Home, "vitals:compact 0,0 110x1 | header:compact 0,1 110x1 | footer:compact 0,29 110x1 | tasks:compact 0,2 60x21 | inbox:compact 0,24 60x5 | lanes:compact 61,2 49x14 | activity:compact 61,17 49x3 | rule 60"},
		{120, 40, Home, "vitals:compact 0,0 120x1 | header:compact 0,1 120x1 | footer:compact 0,39 120x1 | tasks:compact 0,2 66x31 | inbox:compact 0,34 66x5 | lanes:compact 67,2 53x14 | activity:compact 67,17 53x3 | rule 66"},
		{159, 40, Home, "vitals:full 0,0 159x1 | header:full 0,1 159x1 | footer:full 0,39 159x1 | tasks:compact 0,2 87x31 | inbox:compact 0,34 87x5 | lanes:compact 88,2 71x14 | activity:compact 88,17 71x3 | rule 87"},
		{160, 45, Home, "vitals:full 0,0 160x1 | header:full 0,1 160x1 | footer:full 0,44 160x1 | tasks:full 0,2 88x34 | inbox:full 0,37 88x7 | lanes:full 89,2 71x14 | activity:full 89,17 71x5 | rule 88"},
		{170, 45, Home, "vitals:full 0,0 170x1 | header:full 0,1 170x1 | footer:full 0,44 170x1 | tasks:full 0,2 93x34 | inbox:full 0,37 93x7 | lanes:full 94,2 76x14 | activity:full 94,17 76x5 | rule 93"},
		{199, 45, Home, "vitals:full 0,0 199x1 | header:full 0,1 199x1 | footer:full 0,44 199x1 | tasks:full 0,2 110x34 | inbox:full 0,37 110x7 | lanes:full 111,2 88x14 | activity:full 111,17 88x5 | rule 110"},
		{200, 60, Home, "vitals:full 0,0 200x1 | header:full 0,1 200x1 | footer:full 0,59 200x1 | tasks:full 0,2 85x49 | inbox:full 0,52 85x7 | lanes:full 86,2 69x14 | activity:full 86,17 69x5 | inspector:full 156,2 44x57 | rule 85 | rule 155"},
		{220, 60, Home, "vitals:full 0,0 220x1 | header:full 0,1 220x1 | footer:full 0,59 220x1 | tasks:full 0,2 94x49 | inbox:full 0,52 94x7 | lanes:full 95,2 76x14 | activity:full 95,17 76x5 | inspector:full 172,2 48x57 | rule 94 | rule 171"},
		{55, 40, Split, "vitals:strip 0,0 55x1 | notice:strip 0,1 55x1 | inbox:compact 0,2 55x38"},
		{56, 24, Split, "vitals:compact 0,0 56x1 | header:compact 0,1 56x1 | footer:compact 0,23 56x1 | tasks:compact 0,2 56x9 | inbox:compact 0,12 56x5 | lanes:strip 0,18 56x1 | activity:compact 0,20 56x3"},
		{56, 29, Split, "vitals:compact 0,0 56x1 | header:compact 0,1 56x1 | footer:compact 0,28 56x1 | tasks:compact 0,2 56x12 | inbox:compact 0,15 56x5 | lanes:strip 0,21 56x1 | activity:compact 0,23 56x5"},
		{60, 40, Split, "vitals:compact 0,0 60x1 | header:compact 0,1 60x1 | footer:compact 0,39 60x1 | tasks:compact 0,2 60x23 | inbox:compact 0,26 60x5 | lanes:strip 0,32 60x1 | activity:compact 0,34 60x5"},
		{80, 24, Split, "vitals:compact 0,0 80x1 | header:compact 0,1 80x1 | footer:compact 0,23 80x1 | tasks:compact 0,2 80x9 | inbox:compact 0,12 80x5 | lanes:strip 0,18 80x1 | activity:compact 0,20 80x3"},
		{109, 30, Split, "vitals:compact 0,0 109x1 | header:compact 0,1 109x1 | footer:compact 0,29 109x1 | tasks:compact 0,2 109x10 | inbox:compact 0,13 109x5 | lanes:compact 0,19 109x6 | activity:compact 0,26 109x3"},
		{120, 40, Split, "vitals:compact 0,0 120x1 | header:compact 0,1 120x1 | footer:compact 0,39 120x1 | tasks:compact 0,2 66x31 | inbox:compact 0,34 66x5 | lanes:compact 67,2 53x14 | activity:compact 67,17 53x3 | rule 66"},
		{0, 24, Home, ""},
		{80, 1, Home, "notice:strip 0,0 80x1"},
	} {
		if got := describe(Compute(tc.cols, tc.rows, tc.mode)); got != tc.want {
			t.Errorf("%dx%d %s:\n got %s\nwant %s", tc.cols, tc.rows, tc.mode, got, tc.want)
		}
	}
}

// Split sizes its panes by what they hold: an empty lanes, activity or inbox
// pane is one strip row, lanes below ChipLanes are a row of chips, and tasks
// take the rows the others do not need, up to the rows their items fill.
func TestSplitFollowsContent(t *testing.T) {
	for _, tc := range []struct {
		cols, rows int
		c          Counts
		want       string
	}{
		// The live Board beside the chat, before the run has lanes or
		// decisions: three strips, the tasks it has and the inbox below.
		{56, 29, Counts{Tasks: 5}, "vitals:compact 0,0 56x1 | header:compact 0,1 56x1 | footer:compact 0,28 56x1 | tasks:compact 0,2 56x7 | inbox:strip 0,10 56x1 | lanes:strip 0,12 56x1 | activity:strip 0,14 56x1"},
		{56, 29, Counts{}, "vitals:compact 0,0 56x1 | header:compact 0,1 56x1 | footer:compact 0,28 56x1 | tasks:compact 0,2 56x3 | inbox:strip 0,6 56x1 | lanes:strip 0,8 56x1 | activity:strip 0,10 56x1"},
		// Lanes running: still chips at 56, tasks keep the middle.
		{56, 29, Counts{Lanes: 9, Tasks: 11, Inbox: 2, Activity: 4}, "vitals:compact 0,0 56x1 | header:compact 0,1 56x1 | footer:compact 0,28 56x1 | tasks:compact 0,2 56x12 | inbox:compact 0,15 56x5 | lanes:strip 0,21 56x1 | activity:compact 0,23 56x5"},
		// One decision asks for its two rows only.
		{56, 29, Counts{Lanes: 3, Tasks: 40, Inbox: 1, Activity: 2}, "vitals:compact 0,0 56x1 | header:compact 0,1 56x1 | footer:compact 0,28 56x1 | tasks:compact 0,2 56x16 | inbox:compact 0,19 56x3 | lanes:strip 0,23 56x1 | activity:compact 0,25 56x3"},
		// Wide enough for lane rows: as many as there are lanes.
		{100, 40, Counts{Lanes: 3, Tasks: 40, Inbox: 0, Activity: 4}, "vitals:compact 0,0 100x1 | header:compact 0,1 100x1 | footer:compact 0,39 100x1 | tasks:compact 0,2 100x23 | inbox:strip 0,26 100x1 | lanes:compact 0,28 100x5 | activity:compact 0,34 100x5"},
		{100, 40, Counts{Lanes: 0, Tasks: 40, Inbox: 2, Activity: 4}, "vitals:compact 0,0 100x1 | header:compact 0,1 100x1 | footer:compact 0,39 100x1 | tasks:compact 0,2 100x23 | inbox:compact 0,26 100x5 | lanes:strip 0,32 100x1 | activity:compact 0,34 100x5"},
	} {
		if got := describe(ComputeFor(tc.cols, tc.rows, Split, tc.c)); got != tc.want {
			t.Errorf("%dx%d %+v:\n got %s\nwant %s", tc.cols, tc.rows, tc.c, got, tc.want)
		}
	}
	// Home is Compute's layout when the run is busy.
	if a, b := ComputeFor(120, 40, Home, Busy), Compute(120, 40, Home); !reflect.DeepEqual(a, b) {
		t.Fatalf("Home with Busy counts: %s", describe(a))
	}
	// A few tasks and lanes: inbox and activity follow their last rows,
	// and the rows nobody fills stay blank at the bottom.
	if got, want := describe(ComputeFor(170, 45, Home, Counts{Lanes: 9, Tasks: 12, Inbox: 2, Activity: 4})), "vitals:full 0,0 170x1 | header:full 0,1 170x1 | footer:full 0,44 170x1 | tasks:full 0,2 93x14 | inbox:full 0,17 93x7 | lanes:full 94,2 76x11 | activity:full 94,14 76x5 | rule 93"; got != want {
		t.Errorf("Home 170x45 demo:\n got %s\nwant %s", got, want)
	}
}

// Every Split size and content holds the invariants. Below TwoColumns the
// panes stack in the prototype's order, tasks, inbox, lanes, activity; tasks
// grow to the rows their items fill and the inbox follows them; rows nobody
// fills stay blank at the bottom, never in a gap above the inbox.
func TestSplitInvariantsAtEveryContent(t *testing.T) {
	for _, c := range []Counts{{}, {Tasks: 1}, {Lanes: 1, Inbox: 1, Activity: 1}, Busy, {Lanes: 40, Tasks: 400, Inbox: 30, Activity: 50}} {
		for cols := MinTasks; cols <= 200; cols += 3 {
			for rows := MinRows; rows <= 80; rows++ {
				l := ComputeFor(cols, rows, Split, c)
				checkLayout(t, l)
				if cols >= TwoColumns {
					continue
				}
				last := l.Panes[len(l.Panes)-1]
				tasks, _ := l.Pane(Tasks)
				inbox, _ := l.Pane(Inbox)
				if last.ID != Activity || last.Rect.Y+last.Rect.H > l.Body.Y+l.Body.H || inbox.Rect.Y != tasks.Rect.Y+tasks.Rect.H+1 {
					t.Fatalf("%dx%d %+v: inbox does not follow tasks: %s", cols, rows, c, describe(l))
				}
				if last.Rect.Y+last.Rect.H < l.Body.Y+l.Body.H && tasks.Rect.H < 2+c.Tasks {
					t.Fatalf("%dx%d %+v: rows left over while tasks are cut: %s", cols, rows, c, describe(l))
				}
				if lanes, _ := l.Pane(Lanes); (cols < ChipLanes || c.Lanes == 0) && lanes.Density != Strip {
					t.Fatalf("%dx%d %+v: lanes not a strip: %s", cols, rows, c, describe(l))
				}
				if inbox, _ := l.Pane(Inbox); c.Inbox == 0 && inbox.Rect.H != 1 {
					t.Fatalf("%dx%d %+v: empty inbox takes %d rows", cols, rows, c, inbox.Rect.H)
				}
			}
		}
	}
}

// Every size either mode can meet: rects stay on screen, nothing overlaps,
// and the minimums hold wherever the Board is laid out at all.
func TestComputeInvariantsAtEverySize(t *testing.T) {
	for _, mode := range []Mode{Home, Split} {
		for cols := 1; cols <= 260; cols++ {
			for rows := 1; rows <= 80; rows++ {
				l := Compute(cols, rows, mode)
				if !reflect.DeepEqual(l, Compute(cols, rows, mode)) {
					t.Fatalf("%dx%d %s: not a function of its inputs", cols, rows, mode)
				}
				checkLayout(t, l)
			}
		}
	}
}

func checkLayout(t *testing.T, l Layout) {
	t.Helper()
	at := fmt.Sprintf("%dx%d %s", l.Cols, l.Rows, l.Mode)
	screen := Rect{0, 0, l.Cols, l.Rows}
	rects := append([]Rect(nil), l.Rules...)
	seen := map[PaneID]bool{}
	for _, p := range l.Panes {
		if seen[p.ID] {
			t.Fatalf("%s: %s placed twice", at, p.ID)
		}
		seen[p.ID] = true
		rects = append(rects, p.Rect)
	}
	for i, r := range rects {
		if r.W <= 0 || r.H <= 0 || !inside(r, screen) {
			t.Fatalf("%s: %+v empty or off screen", at, r)
		}
		for _, o := range rects[i+1:] {
			if overlaps(r, o) {
				t.Fatalf("%s: %+v overlaps %+v", at, r, o)
			}
		}
	}
	if !seen[Inbox] && l.Rows > 2 {
		t.Fatalf("%s: the inbox is never hidden", at)
	}
	if seen[Notice] {
		minCols := MinCols
		if l.Mode == Split {
			minCols = MinTasks
		}
		if l.Cols >= minCols && l.Rows >= MinRows {
			t.Fatalf("%s: notice at a supported size", at)
		}
		return
	}
	minWidth := map[PaneID]int{Tasks: MinTasks, Lanes: MinLanes, Inspector: MinInspector}
	for _, p := range l.Panes {
		bar := p.ID == Vitals || p.ID == Header || p.ID == Footer
		// The activity of a narrow Home is its heading and one line at 80x24.
		narrowActivity := p.ID == Activity && l.Mode == Home && l.Cols < FullOverlay && p.Rect.H == 2
		if !bar && !narrowActivity && (p.Density == Strip && p.Rect.H != 1 || p.Density != Strip && p.Rect.H < minPaneRows) {
			t.Fatalf("%s: %s %s has %d rows", at, p.ID, p.Density, p.Rect.H)
		}
		if p.Rect.W < minWidth[p.ID] {
			t.Fatalf("%s: %s is %d wide", at, p.ID, p.Rect.W)
		}
		if bar && p.Rect.H != 1 || !bar && !inside(p.Rect, l.Body) {
			t.Fatalf("%s: %s outside the body", at, p.ID)
		}
	}
	wantRules := 0
	if l.Mode == Home && l.Cols >= DockedColumn {
		wantRules = 2
	} else if l.Cols >= TwoColumns {
		// Home, and Split from TwoColumns: the prototype's two columns.
		wantRules = 1
	}
	if len(l.Rules) != wantRules || seen[Inspector] != (wantRules == 2) {
		t.Fatalf("%s: %d columns, inspector docked %v", at, len(l.Rules)+1, seen[Inspector])
	}
	if !seen[Vitals] || !seen[Header] || !seen[Footer] || !seen[Tasks] || !seen[Lanes] || !seen[Activity] {
		t.Fatalf("%s: %s", at, describe(l))
	}
}

// TestVitalsRowOnTop: the vitals row is row 0 on every layout, nothing else
// shares that row, and the body starts at row 2 (the header is row 1).
func TestVitalsRowOnTop(t *testing.T) {
	for _, tc := range []struct {
		cols, rows int
		mode       Mode
	}{{80, 24, Home}, {56, 24, Split}, {200, 60, Home}, {40, 10, Home}} {
		l := Compute(tc.cols, tc.rows, tc.mode)
		v, ok := l.Pane(Vitals)
		if !ok || v.Rect != (Rect{0, 0, tc.cols, 1}) {
			t.Fatalf("%dx%d %s: vitals %+v ok=%v", tc.cols, tc.rows, tc.mode, v.Rect, ok)
		}
		for _, p := range l.Panes {
			if p.ID != Vitals && p.Rect.Y == 0 {
				t.Fatalf("%dx%d %s: %s also at row 0", tc.cols, tc.rows, tc.mode, p.ID)
			}
		}
		if l.Body.Y != 2 {
			t.Fatalf("%dx%d %s: body at row %d", tc.cols, tc.rows, tc.mode, l.Body.Y)
		}
	}
}

// level is how much of a pane a stack keeps: 0 gone, 1 its strip, 2 rows.
func levels(r Rect, slots []slot) map[PaneID]int {
	out := map[PaneID]int{}
	for _, p := range stack(r, slots) {
		out[p.ID] = 1
		if p.Density != Strip {
			out[p.ID] = 2
		}
	}
	return out
}

// As rows run out the lowest priority pane gives way first: a pane is never
// cut back while a pane below it in priority keeps more than its strip.
func TestStackCollapsesLowestPriorityFirst(t *testing.T) {
	for _, cols := range []int{100, 120, 220} {
		for _, c := range columns(cols, Busy, true) {
			full := levels(Rect{0, 0, c.w, 100}, c.slots)
			for h := 1; h <= 40; h++ {
				got := levels(Rect{0, 0, c.w, h}, c.slots)
				for _, a := range c.slots {
					for _, b := range c.slots {
						if priority[a.id] < priority[b.id] && got[a.id] < full[a.id] && got[b.id] == 2 {
							t.Fatalf("%d h=%d: %s cut back while %s keeps its rows: %v", cols, h, a.id, b.id, got)
						}
					}
				}
			}
		}
	}
	one := columns(100, Busy, true)[0]
	for h, want := range map[int]string{
		9: "inbox:full 0,0 100x3 | tasks:strip 0,4 100x1 | lanes:strip 0,6 100x1 | activity:strip 0,8 100x1",
		8: "inbox:full 0,0 100x4 | tasks:strip 0,5 100x1 | lanes:strip 0,7 100x1",
		5: "inbox:full 0,0 100x3 | tasks:strip 0,4 100x1",
		4: "inbox:full 0,0 100x4",
		2: "tasks:strip 0,0 100x1",
	} {
		if got := describe(Layout{Panes: stack(Rect{0, 0, 100, h}, one.slots)}); got != want {
			t.Errorf("h=%d:\n got %s\nwant %s", h, got, want)
		}
	}
}

// The caller keeps one Viewport per pane; a resize re-runs Compute and Fit
// against the new height, and the selection survives every size on the way.
func TestResizeKeepsSelection(t *testing.T) {
	const tasks = 53
	v := Viewport{Sel: 30}
	var tops []int
	for _, size := range [][2]int{{220, 60}, {80, 24}, {120, 40}, {100, 30}, {220, 60}} {
		p, ok := Compute(size[0], size[1], Home).Pane(Tasks)
		if !ok {
			t.Fatalf("%v: no tasks pane", size)
		}
		items := p.Rect.H - 1 // the heading row
		v = v.Fit(items, tasks)
		if v.Sel != 30 || v.Top > v.Sel || v.Sel >= v.Top+items {
			t.Fatalf("%v: %+v does not show the selection in %d rows", size, v, items)
		}
		tops = append(tops, v.Top)
	}
	// Shrinking scrolls just enough to keep the selection, growing keeps the
	// scroll until it would leave rows empty past the last task.
	if want := []int{0, 25, 23, 25, 5}; !reflect.DeepEqual(tops, want) {
		t.Fatalf("tops = %v, want %v", tops, want)
	}
}

func TestViewportFitClampsToItems(t *testing.T) {
	for _, tc := range []struct {
		v       Viewport
		rows, n int
		want    Viewport
	}{
		{Viewport{Top: 3, Sel: 30}, 10, 5, Viewport{Top: 0, Sel: 4}},
		{Viewport{Top: 0, Sel: 9}, 4, 20, Viewport{Top: 6, Sel: 9}},
		{Viewport{Top: 8, Sel: 2}, 4, 20, Viewport{Top: 2, Sel: 2}},
		{Viewport{Top: 5, Sel: 7}, 4, 20, Viewport{Top: 5, Sel: 7}},
		{Viewport{Top: 5, Sel: -1}, 0, 20, Viewport{Top: 0, Sel: 0}},
		{Viewport{Top: 5, Sel: 7}, 4, 0, Viewport{}},
	} {
		if got := tc.v.Fit(tc.rows, tc.n); got != tc.want {
			t.Errorf("%+v.Fit(%d, %d) = %+v, want %+v", tc.v, tc.rows, tc.n, got, tc.want)
		}
	}
}

func TestOverlayStacksInsideTheBody(t *testing.T) {
	for _, mode := range []Mode{Home, Split} {
		for cols := 1; cols <= 260; cols++ {
			for _, rows := range []int{1, 23, 24, 30, 60} {
				l := Compute(cols, rows, mode)
				prev := l.Body
				for depth := range 6 {
					r := l.Overlay(depth)
					if !inside(r, prev) {
						t.Fatalf("%dx%d %s depth %d: %+v outside %+v", cols, rows, mode, depth, r, prev)
					}
					if cols < FullOverlay && r != l.Body {
						t.Fatalf("%dx%d %s: a narrow overlay takes the whole body, got %+v", cols, rows, mode, r)
					}
					if cols >= FullOverlay && rows >= MinRows && (r.W < MinInspector || r == l.Body) {
						t.Fatalf("%dx%d %s depth %d: %+v", cols, rows, mode, depth, r)
					}
					prev = r
				}
			}
		}
	}
	l := Compute(120, 40, Home)
	if a, b := l.Overlay(0), l.Overlay(1); a != (Rect{6, 3, 108, 35}) || b != (Rect{8, 4, 104, 33}) {
		t.Fatalf("overlays at 120x40: %+v, %+v", a, b)
	}
}

func TestSplitWidthsHalveTheWindowOrKeepTheBoardMinimum(t *testing.T) {
	for window := 1; window <= 400; window++ {
		m, b, ok := SplitWidths(window)
		halved := b-m >= 0 && b-m <= 1
		widened := b == MinTasks && m >= MinChat && (window-1)/2 < MinTasks
		if m+1+b != window || !halved && !widened {
			t.Fatalf("%d: %d | %d", window, m, b)
		}
		if ok != (b >= MinTasks) {
			t.Fatalf("%d: ok=%v with a %d-column Board", window, ok, b)
		}
		if _, notice := Compute(b, MinRows, Split).Pane(Notice); ok && notice {
			t.Fatalf("%d: a split the layout cannot fill", window)
		}
	}
	if m, b, ok := SplitWidths(106); !ok || m != 49 || b != MinTasks {
		t.Fatalf("106 columns must give the Board its tasks pane: %d %d %v", m, b, ok)
	}
	if _, _, ok := SplitWidths(96); ok {
		t.Fatal("96 columns cannot give the Board 56 and keep a 40-column chat")
	}
	if m, b, ok := SplitWidths(220); !ok || m != 109 || b != 110 {
		t.Fatalf("220: %d %d %v", m, b, ok)
	}
}

// An empty lanes pane is its heading and column heads, and the activity
// follows it: the spare rows stay blank at the bottom, not above the
// activity (a seeded run with no live worker showed the gap).
func TestEmptyLanesDoNotPushActivityDown(t *testing.T) {
	for _, cols := range []int{120, 170} {
		l := ComputeFor(cols, 34, Home, Counts{Tasks: 12, Lanes: 0, Activity: 4})
		lanes, ok1 := l.Pane(Lanes)
		act, ok2 := l.Pane(Activity)
		if !ok1 || !ok2 {
			t.Fatalf("%d cols: lanes %v activity %v", cols, ok1, ok2)
		}
		if lanes.Rect.H > minPaneRows || act.Rect.Y != lanes.Rect.Y+lanes.Rect.H+1 {
			t.Fatalf("%d cols: lanes %+v, activity %+v", cols, lanes.Rect, act.Rect)
		}
	}
}
