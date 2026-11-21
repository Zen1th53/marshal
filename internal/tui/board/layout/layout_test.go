package layout

import (
	"fmt"
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

func inside(r, o Rect) bool {
	return r.X >= o.X && r.Y >= o.Y && r.X+r.W <= o.X+o.W && r.Y+r.H <= o.Y+o.H
}

// The Board's frame is its bars and a body; below the minimum size it is the
// vitals row and the too-small notice.
func TestComputeAtEveryBreakpointAndMode(t *testing.T) {
	for _, tc := range []struct {
		cols, rows int
		mode       Mode
		want       string
	}{
		{79, 24, Home, "vitals:strip 0,0 79x1 | notice:strip 0,1 79x1"},
		{80, 11, Home, "vitals:strip 0,0 80x1 | notice:strip 0,1 80x1"},
		{80, 12, Home, "vitals:compact 0,0 80x1 | header:compact 0,1 80x1 | footer:compact 0,11 80x1"},
		{120, 40, Home, "vitals:compact 0,0 120x1 | header:compact 0,1 120x1 | footer:compact 0,39 120x1"},
		{160, 45, Home, "vitals:full 0,0 160x1 | header:full 0,1 160x1 | footer:full 0,44 160x1"},
		{59, 40, Split, "vitals:strip 0,0 59x1 | notice:strip 0,1 59x1"},
		{60, 24, Split, "vitals:compact 0,0 60x1 | header:compact 0,1 60x1 | footer:compact 0,23 60x1"},
		{0, 24, Home, ""},
		{80, 1, Home, "notice:strip 0,0 80x1"},
	} {
		if got := describe(Compute(tc.cols, tc.rows, tc.mode)); got != tc.want {
			t.Errorf("%dx%d %s:\n got %s\nwant %s", tc.cols, tc.rows, tc.mode, got, tc.want)
		}
	}
	if l := Compute(120, 40, Home); l.Body != (Rect{0, 2, 120, 37}) {
		t.Fatalf("body %+v", l.Body)
	}
}

func TestOverlayStacksInsideTheBody(t *testing.T) {
	for _, mode := range []Mode{Home, Split} {
		for cols := 1; cols <= 260; cols++ {
			for _, rows := range []int{1, 11, 12, 24, 30, 60} {
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
					if cols >= FullOverlay && rows >= 24 && (r.W < minOverlay || r == l.Body) {
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

// SplitWidths never hands the Board fewer than MinSplit columns with ok
// set, so a split Board is never too small for Compute.
func TestSplitWidthsHalveTheWindowOrKeepTheBoardMinimum(t *testing.T) {
	for window := 1; window <= 400; window++ {
		m, b, ok := SplitWidths(window)
		halved := b-m >= 0 && b-m <= 1
		widened := b == MinSplit && m >= MinChat && (window-1)/2 < MinSplit
		if m+1+b != window || !halved && !widened {
			t.Fatalf("%d: %d | %d", window, m, b)
		}
		if ok != (b >= MinSplit) {
			t.Fatalf("%d: ok=%v with a %d-column Board", window, ok, b)
		}
		if _, notice := Compute(b, MinRows, Split).Pane(Notice); ok && notice {
			t.Fatalf("%d: a split the layout cannot fill", window)
		}
	}
	if m, b, ok := SplitWidths(110); !ok || m != 49 || b != MinSplit {
		t.Fatalf("110 columns must give the Board its minimum: %d %d %v", m, b, ok)
	}
	if _, _, ok := SplitWidths(100); ok {
		t.Fatal("100 columns cannot give the Board 60 and keep a 40-column chat")
	}
	if m, b, ok := SplitWidths(220); !ok || m != 109 || b != 110 {
		t.Fatalf("220: %d %d %v", m, b, ok)
	}
}

// TestWithoutVitals takes the vitals row out of a layout one row taller and
// keeps every other pane where it was, one row up.
func TestWithoutVitals(t *testing.T) {
	for _, tc := range []struct {
		cols, rows int
		mode       Mode
	}{{160, 40, Home}, {100, 30, Home}, {99, 40, Split}, {60, 20, Split}} {
		full := Compute(tc.cols, tc.rows+1, tc.mode)
		l := full.WithoutVitals()
		if l.Rows != tc.rows {
			t.Fatalf("%v: rows %d, want %d", tc, l.Rows, tc.rows)
		}
		if _, ok := l.Pane(Vitals); ok {
			t.Fatalf("%v: vitals still placed", tc)
		}
		if len(l.Panes) != len(full.Panes)-1 || l.Body.Y != full.Body.Y-1 {
			t.Fatalf("%v: panes %d/%d body %d/%d", tc, len(l.Panes), len(full.Panes), l.Body.Y, full.Body.Y)
		}
		for _, p := range l.Panes {
			q, _ := full.Pane(p.ID)
			if p.Rect.Y != q.Rect.Y-1 || p.Rect.Y < 0 || p.Rect.Y+p.Rect.H > l.Rows {
				t.Fatalf("%v: %s at %+v (was %+v)", tc, p.ID, p.Rect, q.Rect)
			}
		}
	}
	if l := (Layout{Cols: 10, Rows: 1, Panes: []Pane{{Notice, Rect{0, 0, 10, 1}, Strip}}}); len(l.WithoutVitals().Panes) != 1 {
		t.Fatal("a layout without a vitals row changed")
	}
}
