package architecturereview

import (
	"math"
	"testing"
	"time"
)

// contentScale is the scale the page's Fit gives a drawing on a 1920×1080
// screen (canvas 1920×1018, the same paddings as fitView).
func contentScale(l Layout) float64 {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, n := range l.Nodes {
		x0, y0 = math.Min(x0, n.X), math.Min(y0, n.Y)
		x1, y1 = math.Max(x1, n.X+n.Width), math.Max(y1, n.Y+n.Height)
	}
	w, h := x1-x0+40, y1-y0+68
	return math.Min((1920-48)/w, (1018-82)/h)
}

// The owner's 17-part architecture reads at Fit: part names (16px in the
// drawing) are at least 13px on a 1920px screen.
func TestSeventeenPartDrawingReadsAtFit(t *testing.T) {
	views, err := GenericLayouts(marshalShapeFixture(false))
	if err != nil {
		t.Fatal(err)
	}
	top := views["architecture"]
	if len(top.Nodes) != 17 {
		t.Fatalf("top level has %d parts, want 17", len(top.Nodes))
	}
	if size := 16 * contentScale(top); size < 13 {
		t.Fatalf("part names are %.1fpx at Fit, want at least 13px (drawing %.0f×%.0f)", size, top.Width, top.Height)
	}
	for _, n := range top.Nodes {
		if n.Width < 150 || n.Width > 260 {
			t.Errorf("part %s is %.0f wide, outside the name-sized bounds", n.ID, n.Width)
		}
	}
}

// 80 parts and 300 connections lay out quickly, validate and stay bounded.
func TestLargeDrawingStaysBounded(t *testing.T) {
	raw := generatedFixture(80, 300)
	start := time.Now()
	views, err := GenericLayouts(raw)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("layout took %v", elapsed)
	}
	a, err := decodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLayouts(a, views); err != nil {
		t.Fatal(err)
	}
	l := views["architecture"]
	if len(l.Nodes) != 80 || len(l.Edges) != 300 {
		t.Fatalf("drawing has %d parts and %d connections", len(l.Nodes), len(l.Edges))
	}
	if l.Width > 6000 || l.Height > 6000 {
		t.Fatalf("drawing is %.0f×%.0f, larger than the viewer is meant to pan", l.Width, l.Height)
	}
}
