package architecturecodec

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func roomyFixture() json.RawMessage {
	raw := architectureJSON([]string{"client", "services", "storage", "browser", "console", "gateway", "worker", "auth", "cache", "database", "archive", "query", "result"}, [][3]string{{"browser", "gateway", "requests"}, {"console", "gateway", "commands"}, {"gateway", "auth", "verify"}, {"gateway", "worker", "dispatch"}, {"worker", "database", "persist"}, {"worker", "cache", "lookup"}, {"database", "archive", "snapshot"}, {"archive", "worker", "restore"}, {"query", "result", "results"}, {"query", "result", "metadata"}})
	var a map[string]any
	_ = json.Unmarshal(raw, &a)
	parents := map[string]string{"browser": "client", "console": "client", "gateway": "services", "worker": "services", "auth": "services", "cache": "storage", "database": "storage", "archive": "storage", "query": "gateway", "result": "database"}
	for _, v := range a["components"].([]any) {
		c := v.(map[string]any)
		if p := parents[c["id"].(string)]; p != "" {
			c["parent"] = p
			c["responsibility"] = map[string]string{"browser": "Owner web interface", "console": "Operator terminal", "gateway": "Routes incoming requests", "worker": "Executes background jobs", "auth": "Verifies identity", "cache": "Fast lookup storage", "database": "Persistent records", "archive": "Retained snapshots", "query": "Query input", "result": "Result output"}[c["id"].(string)]
		}
	}
	for _, v := range a["connections"].([]any) {
		c := v.(map[string]any)
		c["access"] = "RW"
		c["protocol"] = "HTTPS"
	}
	raw, _ = json.Marshal(a)
	return raw
}

func roomyLargeFixture() json.RawMessage {
	var ids []string
	parents := map[string]string{}
	groups := [][]string{}
	for area, count := range []int{5, 3, 2, 2, 2, 2} {
		root := fmt.Sprintf("area%d", area)
		ids = append(ids, root)
		var group []string
		for j := 0; j < count; j++ {
			id := fmt.Sprintf("block%d_%d", area, j)
			ids = append(ids, id)
			parents[id] = root
			group = append(group, id)
		}
		groups = append(groups, group)
	}
	var edges [][3]string
	for area := 0; area < 5; area++ {
		for j := 0; j < 3; j++ {
			edges = append(edges, [3]string{groups[area][j%len(groups[area])], groups[area+1][j%len(groups[area+1])], "handoff"})
		}
	}
	for area := 0; area < 6; area++ {
		edges = append(edges, [3]string{groups[area][0], groups[area][1], "local call"})
	}
	edges = append(edges, [3]string{groups[5][0], groups[1][0], "feedback"}, [3]string{groups[0][0], groups[3][0], "events"}, [3]string{groups[2][0], groups[5][0], "archive"})
	var a map[string]any
	_ = json.Unmarshal(architectureJSON(ids, edges), &a)
	for _, v := range a["components"].([]any) {
		c := v.(map[string]any)
		id := c["id"].(string)
		c["responsibility"] = "Contract component"
		if p := parents[id]; p != "" {
			c["parent"] = p
		}
	}
	raw, _ := json.Marshal(a)
	return raw
}

func TestRoomyGeometry(t *testing.T) {
	cases := map[string]json.RawMessage{"compound": roomyFixture(), "large": roomyLargeFixture()}
	for name, raw := range layoutCases {
		cases[name] = raw
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			l, err := RoomyLayout(raw)
			if err != nil {
				t.Fatal(err)
			}
			again, err := RoomyLayout(raw)
			if err != nil || !reflect.DeepEqual(l, again) {
				t.Fatal("layout is not deterministic")
			}
			a, _ := decodeObject(raw)
			parents := ParentsOf(a)
			nodes := map[string]Node{}
			for _, n := range l.Nodes {
				nodes[n.ID] = n.Node
				if n.X < 0 || n.Y < 0 || n.X+n.Width > l.Width || n.Y+n.Height > l.Height {
					t.Fatalf("node outside drawing: %s", n.ID)
				}
				if p := parents[n.ID]; p != "" {
					parent := nodes[p]
					if n.X < parent.X || n.Y < parent.Y+80 || n.X+n.Width > parent.X+parent.Width || n.Y+n.Height > parent.Y+parent.Height {
						t.Fatalf("child outside parent: %s", n.ID)
					}
				}
			}
			var segments [][2][2]float64
			for _, e := range l.Edges {
				segments = append(segments, segmentsOf(e)...)
			}
			for i, p := range segments {
				for _, q := range segments[i+1:] {
					if collinearOverlap(p, q) {
						t.Fatalf("routing lanes share a segment: %v / %v", p, q)
					}
				}
			}
			var labelRects []Rect
			for i, e := range l.Edges {
				if len(e.Points) > 6 {
					t.Fatalf("more than four bends: %s", e.ID)
				}
				c := a["connections"].([]any)[i].(map[string]any)
				s, tgt := nodes[c["source"].(string)], nodes[c["target"].(string)]
				if !onBoundary(e.Points[0], s) || !onBoundary(e.Points[len(e.Points)-1], tgt) {
					t.Fatalf("wrong endpoints: %s", e.ID)
				}
				label := Rect{X: e.LabelAt[0] - textExtent(e.Caption)/2 - 12, Y: e.LabelAt[1] - 18, Width: textExtent(e.Caption) + 24, Height: 26}
				for _, r := range labelRects {
					if label.X < r.X+r.Width && label.X+label.Width > r.X && label.Y < r.Y+r.Height && label.Y+label.Height > r.Y {
						t.Fatalf("overlapping labels: %s", e.ID)
					}
				}
				labelRects = append(labelRects, label)
				if e.Points[0][0] != s.X+s.Width || e.Points[len(e.Points)-1][0] != tgt.X {
					t.Fatalf("incorrect endpoint side: %s", e.ID)
				}
				for _, n := range l.Nodes {
					if !n.Area && label.X < n.X+n.Width && label.X+label.Width > n.X && label.Y < n.Y+n.Height && label.Y+label.Height > n.Y {
						t.Fatalf("label intersects node: %s / %s", e.ID, n.ID)
					}
				}
				for j := 1; j < len(e.Points); j++ {
					p, q := e.Points[j-1], e.Points[j]
					if p[0] != q[0] && p[1] != q[1] {
						t.Fatalf("nonorthogonal: %s", e.ID)
					}
					for _, n := range l.Nodes {
						if Contains(parents, n.ID, s.ID) || Contains(parents, n.ID, tgt.ID) {
							continue
						}
						if strictlyInside([2][2]float64{p, q}, n.Node) {
							t.Fatalf("edge intersects block: %s / %s", e.ID, n.ID)
						}
					}
				}
			}
		})
	}
}
func TestRoomyGoldenSVG(t *testing.T) {
	svg, err := RenderSVG(roomyFixture(), RendererOptions{Roomy: true})
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_ROOMY_GOLDEN") == "1" {
		if err := os.WriteFile("testdata/roomy.svg", []byte(svg), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/roomy.svg")
	if err != nil {
		t.Fatal(err)
	}
	if svg != string(want) {
		t.Fatal("roomy SVG differs from golden")
	}
	decoder := xml.NewDecoder(strings.NewReader(svg))
	ids := map[string]bool{}
	count := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if element, ok := token.(xml.StartElement); ok {
			for _, a := range element.Attr {
				if a.Name.Local == "id" {
					if ids[a.Value] {
						t.Fatalf("duplicate SVG id: %s", a.Value)
					}
					ids[a.Value] = true
				}
				if a.Name.Local == "data-id" {
					count++
				}
			}
		}
	}
	for role, want := range map[string]int{"area": 3, "block": 8, "chip": 2} {
		if got := strings.Count(svg, `data-role="`+role+`"`); got != want {
			t.Fatalf("%s count: %d", role, got)
		}
	}
	if count != 23 {
		t.Fatalf("want 13 nodes and 10 edges, got %d", count)
	}
	if _, err := RenderSVG(roomyFixture(), RendererOptions{}); err == nil {
		t.Fatal("roomy renderer must be gated")
	}
}

func TestRoomyRankAndExports(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{"roomy-layout": roomyFixture(), "roomy-layout-large": roomyLargeFixture()} {
		l, err := RoomyLayout(raw)
		if err != nil {
			t.Fatal(err)
		}
		if l.Width <= l.Height {
			t.Fatal("expected landscape layout")
		}
		a, _ := decodeObject(raw)
		parents := ParentsOf(a)
		roots := []string{}
		boxes := map[string]CompoundNode{}
		for _, n := range l.Nodes {
			boxes[n.ID] = n
			if n.Area {
				roots = append(roots, n.ID)
			}
		}
		weights := make([][]int, len(roots))
		idx := map[string]int{}
		for i, id := range roots {
			idx[id] = i
			weights[i] = make([]int, len(roots))
		}
		root := func(id string) string {
			for parents[id] != "" {
				id = parents[id]
			}
			return id
		}
		for _, v := range a["connections"].([]any) {
			c := v.(map[string]any)
			s, tgt := root(c["source"].(string)), root(c["target"].(string))
			if s != tgt {
				weights[idx[s]][idx[tgt]]++
			}
		}
		order := feedbackOrder(weights)
		for i, u := range order {
			for _, v := range order[i+1:] {
				if weights[u][v]+weights[v][u] > 0 && boxes[roots[u]].X >= boxes[roots[v]].X {
					t.Fatal("areas out of rank order")
				}
			}
		}
		svg, err := RenderSVG(raw, RendererOptions{Roomy: true})
		if err != nil {
			t.Fatal(err)
		}
		golden := "testdata/" + name + ".svg"
		if name == "roomy-layout-large" {
			if os.Getenv("UPDATE_ROOMY_GOLDEN") == "1" {
				if err := os.WriteFile(golden, []byte(svg), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != svg {
				t.Fatal("large SVG differs from golden")
			}
			if strings.Count(svg, `data-role="area"`) != 6 || strings.Count(svg, `data-role="block"`) != 16 || len(l.Edges) != 24 {
				t.Fatal("large fixture counts changed")
			}
		}
		if dest := os.Getenv("ROOMY_EXPORT_DIR"); dest != "" {
			if err := os.WriteFile(dest+"/"+name+".svg", []byte(svg), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestRoomyEqualRanksAndFeedbackMinimum(t *testing.T) {
	raw := architectureJSON([]string{"left", "peer", "sink"}, [][3]string{{"left", "sink", "call"}, {"peer", "sink", "call"}})
	l, err := RoomyLayout(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]Node{}
	for _, n := range l.Nodes {
		nodes[n.ID] = n.Node
	}
	if nodes["left"].X != nodes["peer"].X || nodes["left"].Y == nodes["peer"].Y || nodes["sink"].X <= nodes["left"].X {
		t.Fatal("equal ranks must share a column and stack vertically")
	}
	for _, n := range []int{3, 19} {
		w := make([][]int, n)
		for i := range w {
			w[i] = make([]int, n)
		}
		w[0][1] = 4
		w[1][2] = 4
		w[2][0] = 1
		order := feedbackOrder(w)
		cost := 0
		for i, u := range order {
			for _, v := range order[i+1:] {
				cost += w[v][u]
			}
		}
		if cost != 1 {
			t.Fatalf("feedback cost=%d, want minimum 1", cost)
		}
	}
}
