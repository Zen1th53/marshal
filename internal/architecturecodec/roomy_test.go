package architecturecodec

import (
	"encoding/json"
	"encoding/xml"
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

func TestRoomyGeometry(t *testing.T) {
	cases := map[string]json.RawMessage{"compound": roomyFixture()}
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
						t.Fatal("routing lanes share a segment")
					}
				}
			}
			for i, e := range l.Edges {
				c := a["connections"].([]any)[i].(map[string]any)
				s, tgt := nodes[c["source"].(string)], nodes[c["target"].(string)]
				if !onBoundary(e.Points[0], s) || !onBoundary(e.Points[len(e.Points)-1], tgt) {
					t.Fatalf("wrong endpoints: %s", e.ID)
				}
				label := Rect{X: e.LabelAt[0] - textExtent(e.Caption)/2, Y: e.LabelAt[1] - 14, Width: textExtent(e.Caption), Height: 20}
				for _, n := range l.Nodes {
					if label.X < n.X+n.Width && label.X+label.Width > n.X && label.Y < n.Y+n.Height && label.Y+label.Height > n.Y {
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
