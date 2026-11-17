package architecturecodec

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func architectureJSON(components []string, connections [][3]string) json.RawMessage {
	var cs, es []string
	for _, id := range components {
		cs = append(cs, fmt.Sprintf(`{"id":%q,"name":%q,"responsibility":"r","technology":"t","files":[],"evidence":[]}`, id, id))
	}
	for i, c := range connections {
		es = append(es, fmt.Sprintf(`{"id":"c%d","source":%q,"target":%q,"label":%q,"protocol":"p"}`, i, c[0], c[1], c[2]))
	}
	return json.RawMessage(`{"title":"T","purpose":"P","summary":"S","components":[` + strings.Join(cs, ",") +
		`],"connections":[` + strings.Join(es, ",") + `],"constraints":[],"assumptions":[],"workers":[]}`)
}

var layoutCases = map[string]json.RawMessage{
	"shop": architectureJSON(
		[]string{"web", "api", "auth", "catalog", "orders", "pay", "db", "queue"},
		[][3]string{{"web", "api", "HTTPS"}, {"api", "auth", "verify token"}, {"api", "catalog", "list products"},
			{"api", "orders", "place order"}, {"orders", "pay", "charge"}, {"orders", "db", "write order"},
			{"catalog", "db", "read"}, {"pay", "queue", "payment event"}, {"queue", "orders", "mark paid"},
			{"auth", "db", "users"}}),
	"chain with skip": architectureJSON(
		[]string{"a", "b", "c", "d", "e"},
		[][3]string{{"a", "b", "1"}, {"b", "c", "2"}, {"c", "d", "3"}, {"d", "e", "4"}, {"a", "e", "skip all"}, {"b", "e", "skip"}}),
	"cycle and self loop": architectureJSON(
		[]string{"x", "y", "z"},
		[][3]string{{"x", "y", "go"}, {"y", "z", "on"}, {"z", "x", "back"}, {"y", "y", "retry"}}),
	"fan out and in": architectureJSON(
		[]string{"src", "w1", "w2", "w3", "w4", "sink", "lonely"},
		[][3]string{{"src", "w1", "job"}, {"src", "w2", "job"}, {"src", "w3", "job"}, {"src", "w4", "job"},
			{"w1", "sink", "done"}, {"w2", "sink", "done"}, {"w3", "sink", "done"}, {"w4", "sink", "done"}}),
	"parallel duplicates": architectureJSON(
		[]string{"a", "b"},
		[][3]string{{"a", "b", "data"}, {"a", "b", "control"}, {"b", "a", "ack"}}),
	"single": architectureJSON([]string{"only"}, nil),
}

func segmentsOf(e Edge) [][2][2]float64 {
	var s [][2][2]float64
	for i := 1; i < len(e.Points); i++ {
		s = append(s, [2][2]float64{e.Points[i-1], e.Points[i]})
	}
	return s
}

// strictlyInside reports whether a segment passes through a box's interior.
func strictlyInside(s [2][2]float64, n Node) bool {
	a, b := s[0], s[1]
	if a[0] == b[0] {
		lo, hi := math.Min(a[1], b[1]), math.Max(a[1], b[1])
		return a[0] > n.X && a[0] < n.X+n.Width && hi > n.Y && lo < n.Y+n.Height
	}
	lo, hi := math.Min(a[0], b[0]), math.Max(a[0], b[0])
	return a[1] > n.Y && a[1] < n.Y+n.Height && hi > n.X && lo < n.X+n.Width
}

func collinearOverlap(p, q [2][2]float64) bool {
	if p[0][0] == p[1][0] && q[0][0] == q[1][0] && p[0][0] == q[0][0] {
		return math.Max(math.Min(p[0][1], p[1][1]), math.Min(q[0][1], q[1][1])) < math.Min(math.Max(p[0][1], p[1][1]), math.Max(q[0][1], q[1][1]))
	}
	if p[0][1] == p[1][1] && q[0][1] == q[1][1] && p[0][1] == q[0][1] {
		return math.Max(math.Min(p[0][0], p[1][0]), math.Min(q[0][0], q[1][0])) < math.Min(math.Max(p[0][0], p[1][0]), math.Max(q[0][0], q[1][0]))
	}
	return false
}

func TestGenericLayoutIsReadable(t *testing.T) {
	for name, raw := range layoutCases {
		t.Run(name, func(t *testing.T) {
			views, err := GenericLayouts(raw)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := decodeObject(raw)
			if err := validateLayouts(a, views); err != nil {
				t.Fatalf("layout does not validate: %v", err)
			}
			for viewID, v := range views {
				t.Run(viewID, func(t *testing.T) { checkReadable(t, a, v) })
			}
		})
	}
}

func checkReadable(t *testing.T, a map[string]any, v Layout) {
	t.Helper()
	{
		{
			ends := map[string][2]string{}
			for _, value := range a["connections"].([]any) {
				c := value.(map[string]any)
				ends[c["id"].(string)] = [2]string{c["source"].(string), c["target"].(string)}
			}
			for i, n := range v.Nodes {
				for _, m := range v.Nodes[i+1:] {
					if n.X < m.X+m.Width && m.X < n.X+n.Width && n.Y < m.Y+m.Height && m.Y < n.Y+n.Height {
						t.Errorf("boxes %s and %s overlap", n.ID, m.ID)
					}
				}
			}
			for _, e := range v.Edges {
				for _, s := range segmentsOf(e) {
					for _, n := range v.Nodes {
						if strictlyInside(s, n) {
							t.Errorf("line %s (%s->%s) runs through box %s: %v", e.ID, ends[e.ID][0], ends[e.ID][1], n.ID, e.Points)
						}
					}
				}
			}
			for i, e := range v.Edges {
				for _, f := range v.Edges[i+1:] {
					for _, s := range segmentsOf(e) {
						for _, r := range segmentsOf(f) {
							if collinearOverlap(s, r) {
								t.Errorf("lines %s and %s share a segment: %v / %v", e.ID, f.ID, s, r)
							}
						}
					}
				}
			}
			type box struct{ x0, x1, y0, y1 float64 }
			var captions []box
			for _, e := range v.Edges {
				w := math.Min(6.2*float64(len([]rune(e.Caption))), 170)
				b := box{e.LabelAt[0] - w/2, e.LabelAt[0] + w/2, e.LabelAt[1] - 11, e.LabelAt[1] + 2}
				for _, c := range captions {
					if b.x0 < c.x1 && c.x0 < b.x1 && b.y0 < c.y1 && c.y0 < b.y1 {
						t.Errorf("caption of %s overlaps another caption", e.ID)
					}
				}
				captions = append(captions, b)
			}
		}
	}
}

func TestGenericLayoutFlowsLeftToRight(t *testing.T) {
	views, err := GenericLayouts(layoutCases["shop"])
	if err != nil {
		t.Fatal(err)
	}
	x := map[string]float64{}
	for _, n := range views["architecture"].Nodes {
		x[n.ID] = n.X
	}
	for _, pair := range [][2]string{{"web", "api"}, {"api", "orders"}, {"orders", "pay"}, {"orders", "db"}, {"catalog", "db"}} {
		if x[pair[0]] >= x[pair[1]] {
			t.Errorf("%s (x=%v) should be left of %s (x=%v)", pair[0], x[pair[0]], pair[1], x[pair[1]])
		}
	}
}

func TestGenericLayoutIsDeterministic(t *testing.T) {
	first, _ := json.Marshal(must(GenericLayouts(layoutCases["shop"])))
	for i := 0; i < 5; i++ {
		again, _ := json.Marshal(must(GenericLayouts(layoutCases["shop"])))
		if string(first) != string(again) {
			t.Fatal("the same architecture produced different drawings")
		}
	}
}

func must(v map[string]Layout, err error) map[string]Layout {
	if err != nil {
		panic(err)
	}
	return v
}

func hierarchyJSON() json.RawMessage {
	return json.RawMessage(`{"title":"T","purpose":"P","summary":"S","components":[
{"id":"web","name":"Website","responsibility":"r","technology":"t","files":[],"evidence":[]},
{"id":"login","name":"Login","responsibility":"r","technology":"t","files":[],"evidence":[],"parent":"web"},
{"id":"signup","name":"Sign up","responsibility":"r","technology":"t","files":[],"evidence":[],"parent":"web"},
{"id":"captcha","name":"Captcha","responsibility":"r","technology":"t","files":[],"evidence":[],"parent":"signup"},
{"id":"db","name":"DB","responsibility":"r","technology":"t","files":[],"evidence":[]},
{"id":"api","name":"API","responsibility":"r","technology":"t","files":[],"evidence":[]}],
"connections":[
{"id":"c1","source":"login","target":"db","label":"users","protocol":"p"},
{"id":"c2","source":"signup","target":"login","label":"after sign up","protocol":"p"},
{"id":"c3","source":"api","target":"web","label":"serve","protocol":"p"},
{"id":"c4","source":"captcha","target":"api","label":"verify","protocol":"p"}],
"constraints":[],"assumptions":[],"workers":[]}`)
}

func TestSubArchitecturesGetTheirOwnDrawing(t *testing.T) {
	raw := hierarchyJSON()
	views, err := GenericLayouts(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := decodeObject(raw)
	if err := validateLayouts(a, views); err != nil {
		t.Fatal(err)
	}
	nodes := func(view string) map[string]bool {
		got := map[string]bool{}
		for _, n := range views[view].Nodes {
			got[n.ID] = true
		}
		return got
	}
	edges := func(view string) map[string]bool {
		got := map[string]bool{}
		for _, e := range views[view].Edges {
			got[e.ID] = true
		}
		return got
	}
	want := map[string][2][]string{
		"architecture": {{"web", "db", "api"}, {"c1", "c3", "c4"}},
		"sub:web":      {{"login", "signup"}, {"c2"}},
		"sub:signup":   {{"captcha"}, {}},
	}
	if len(views) != len(want) {
		t.Fatalf("views = %v", len(views))
	}
	for view, w := range want {
		n, e := nodes(view), edges(view)
		if len(n) != len(w[0]) || len(e) != len(w[1]) {
			t.Errorf("%s: nodes %v edges %v", view, n, e)
		}
		for _, id := range w[0] {
			if !n[id] {
				t.Errorf("%s lacks part %s", view, id)
			}
		}
		for _, id := range w[1] {
			if !e[id] {
				t.Errorf("%s lacks connection %s", view, id)
			}
		}
		checkReadable(t, a, views[view])
	}
}

func TestHierarchyRefusals(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"unknown parent": func(s string) string { return strings.Replace(s, `"parent":"web"}`, `"parent":"nope"}`, 1) },
		"self parent": func(s string) string {
			return strings.Replace(s, `"id":"db","name":"DB","responsibility":"r","technology":"t","files":[],"evidence":[]}`, `"id":"db","name":"DB","responsibility":"r","technology":"t","files":[],"evidence":[],"parent":"db"}`, 1)
		},
		"cycle": func(s string) string {
			return strings.Replace(s, `{"id":"web","name":"Website","responsibility":"r","technology":"t","files":[],"evidence":[]}`, `{"id":"web","name":"Website","responsibility":"r","technology":"t","files":[],"evidence":[],"parent":"captcha"}`, 1)
		},
		"part joins its container": func(s string) string {
			return strings.Replace(s, `"source":"login","target":"db"`, `"source":"login","target":"web"`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw := json.RawMessage(edit(string(hierarchyJSON())))
			if string(raw) == string(hierarchyJSON()) {
				t.Fatal("fixture edit did not apply")
			}
			if _, err := GenericLayouts(raw); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
