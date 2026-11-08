package architecturecodec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
)

type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}
type Node struct {
	ID     string  `json:"id"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}
type Edge struct {
	ID        string       `json:"id"`
	Points    [][2]float64 `json:"points"`
	LabelAt   [2]float64   `json:"label_at"`
	Caption   string       `json:"caption"`
	Secondary bool         `json:"secondary"`
}
type Layout struct {
	Version string  `json:"version"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Nodes   []Node  `json:"nodes"`
	Edges   []Edge  `json:"edges"`
}

// BindStoredLayouts validates the original persisted geometry and applies the
// same version binding used by Review. The returned original is suitable for
// PersistDraft; the versioned copy and digest are derived from it.
func BindStoredLayouts(architecture, raw json.RawMessage) (map[string]Layout, map[string]Layout, string, error) {
	a, err := decodeObject(architecture)
	if err != nil {
		return nil, nil, "", err
	}
	if err = validateArchitecture(a); err != nil {
		return nil, nil, "", err
	}
	object, err := decodeObject(raw)
	if err != nil {
		return nil, nil, "", err
	}
	if len(object) != 1 {
		return nil, nil, "", errors.New("architecture drawing views are invalid")
	}
	view, ok := object["architecture"].(map[string]any)
	if !ok || !layoutFields(view, map[string]string{"version": "string", "width": "number", "height": "number", "nodes": "array", "edges": "array"}) {
		return nil, nil, "", errors.New("architecture drawing fields are invalid")
	}
	for _, value := range view["nodes"].([]any) {
		if !layoutFields(value, map[string]string{"id": "string", "x": "number", "y": "number", "width": "number", "height": "number"}) {
			return nil, nil, "", errors.New("drawing node fields are invalid")
		}
	}
	for _, value := range view["edges"].([]any) {
		if !layoutFields(value, map[string]string{"id": "string", "points": "array", "label_at": "array", "caption": "string", "secondary": "bool"}) {
			return nil, nil, "", errors.New("drawing edge fields are invalid")
		}
		edge := value.(map[string]any)
		label := edge["label_at"].([]any)
		if len(label) != 2 || !layoutNumbers(label) {
			return nil, nil, "", errors.New("drawing edge label is invalid")
		}
		for _, point := range edge["points"].([]any) {
			coordinates, ok := point.([]any)
			if !ok || len(coordinates) != 2 || !layoutNumbers(coordinates) {
				return nil, nil, "", errors.New("drawing edge point is invalid")
			}
		}
	}
	var original map[string]Layout
	if err = json.Unmarshal(raw, &original); err != nil {
		return nil, nil, "", err
	}
	if err = validateLayouts(a, original); err != nil {
		return nil, nil, "", err
	}
	bound, digest, err := bindLayoutVersions(original)
	return original, bound, digest, err
}

func layoutFields(value any, fields map[string]string) bool {
	object, ok := value.(map[string]any)
	if !ok || len(object) != len(fields) {
		return false
	}
	for key, kind := range fields {
		v, exists := object[key]
		if !exists {
			return false
		}
		switch kind {
		case "string":
			_, ok = v.(string)
		case "number":
			_, ok = v.(json.Number)
		case "array":
			_, ok = v.([]any)
		case "bool":
			_, ok = v.(bool)
		}
		if !ok {
			return false
		}
	}
	return true
}

func layoutNumbers(values []any) bool {
	for _, value := range values {
		if _, ok := value.(json.Number); !ok {
			return false
		}
	}
	return true
}

// GenericLayouts places declared objects; it never supplies missing topology.
func GenericLayouts(proposal json.RawMessage) (map[string]Layout, error) {
	a, err := decodeObject(proposal)
	if err != nil {
		return nil, err
	}
	if err = validateArchitecture(a); err != nil {
		return nil, err
	}
	components := a["components"].([]any)
	columns := 3
	if len(components) < columns {
		columns = len(components)
	}
	view := Layout{Version: "generic-v1", Width: float64(columns*280 + 160), Height: float64(((len(components)+columns-1)/columns)*200 + 160), Nodes: []Node{}, Edges: []Edge{}}
	byID := map[string]Node{}
	for i, value := range components {
		c := value.(map[string]any)
		n := Node{ID: c["id"].(string), X: float64(80 + (i%columns)*280), Y: float64(80 + (i/columns)*200), Width: 180, Height: 88}
		view.Nodes = append(view.Nodes, n)
		byID[n.ID] = n
	}
	for _, value := range a["connections"].([]any) {
		c := value.(map[string]any)
		source, target := byID[c["source"].(string)], byID[c["target"].(string)]
		start := [2]float64{source.X + source.Width, source.Y + source.Height/2}
		end := [2]float64{target.X, target.Y + target.Height/2}
		mid := (start[0] + end[0]) / 2
		points := [][2]float64{start, {mid, start[1]}, {mid, end[1]}, end}
		if source.ID == target.ID {
			points = [][2]float64{start, {start[0] + 24, start[1]}, {start[0] + 24, source.Y - 24}, {source.X - 24, source.Y - 24}, {source.X - 24, end[1]}, end}
		}
		clean := [][2]float64{}
		for _, point := range points {
			if len(clean) == 0 || clean[len(clean)-1] != point {
				clean = append(clean, point)
			}
		}
		view.Edges = append(view.Edges, Edge{ID: c["id"].(string), Points: clean, LabelAt: [2]float64{mid, (start[1]+end[1])/2 - 12}, Caption: c["label"].(string)})
	}
	return map[string]Layout{"architecture": view}, nil
}

func validateLayouts(a map[string]any, views map[string]Layout) error {
	if len(views) == 0 {
		return errors.New("architecture drawing geometry is missing")
	}
	components, connections := map[string]map[string]any{}, map[string]map[string]any{}
	for _, v := range a["components"].([]any) {
		c := v.(map[string]any)
		components[c["id"].(string)] = c
	}
	for _, v := range a["connections"].([]any) {
		c := v.(map[string]any)
		connections[c["id"].(string)] = c
	}
	seenNodes, seenEdges := map[string]bool{}, map[string]bool{}
	for diagram, view := range views {
		if diagram == "" || view.Version == "" || !finite(view.Width, view.Height) || view.Width <= 0 || view.Height <= 0 {
			return errors.New("invalid architecture drawing identity or bounds")
		}
		nodes := map[string]Node{}
		for _, n := range view.Nodes {
			if components[n.ID] == nil || nodes[n.ID].ID != "" || !finite(n.X, n.Y, n.Width, n.Height) || n.X < 0 || n.Y < 0 || n.Width <= 0 || n.Height <= 0 || n.X+n.Width > view.Width || n.Y+n.Height > view.Height {
				return fmt.Errorf("invalid or ambiguous drawing node %q", n.ID)
			}
			nodes[n.ID] = n
			seenNodes[n.ID] = true
		}
		edges := map[string]bool{}
		for _, e := range view.Edges {
			connection := connections[e.ID]
			if connection == nil || edges[e.ID] || len(e.Points) < 2 || !finite(e.LabelAt[:]...) {
				return fmt.Errorf("invalid drawing edge %q", e.ID)
			}
			source, target := nodes[connection["source"].(string)], nodes[connection["target"].(string)]
			if source.ID == "" || target.ID == "" || !onBoundary(e.Points[0], source) || !onBoundary(e.Points[len(e.Points)-1], target) {
				return fmt.Errorf("drawing edge %q does not terminate at its declared endpoints", e.ID)
			}
			for i, p := range e.Points {
				if !finite(p[:]...) || p[0] < 0 || p[1] < 0 || p[0] > view.Width || p[1] > view.Height {
					return errors.New("edge geometry is outside the drawing")
				}
				if i > 0 && p[0] != e.Points[i-1][0] && p[1] != e.Points[i-1][1] {
					return errors.New("edge geometry must be orthogonal")
				}
			}
			edges[e.ID] = true
			seenEdges[e.ID] = true
		}
	}
	if len(seenNodes) != len(components) || len(seenEdges) != len(connections) {
		return errors.New("every architecture object requires explicit drawing geometry")
	}
	return nil
}

func bindLayoutVersions(views map[string]Layout) (map[string]Layout, string, error) {
	raw, err := json.Marshal(views)
	if err != nil {
		return nil, "", err
	}
	var cloned map[string]Layout
	if err = json.Unmarshal(raw, &cloned); err != nil {
		return nil, "", err
	}
	for id, view := range cloned {
		data, err := json.Marshal(view)
		if err != nil {
			return nil, "", err
		}
		sum := sha256.Sum256(data)
		view.Version = view.Version + ":" + hex.EncodeToString(sum[:])
		cloned[id] = view
	}
	data, err := json.Marshal(cloned)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return cloned, hex.EncodeToString(sum[:]), nil
}

func finite(values ...float64) bool {
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}
func onBoundary(p [2]float64, n Node) bool {
	return ((p[0] == n.X || p[0] == n.X+n.Width) && p[1] >= n.Y && p[1] <= n.Y+n.Height) || ((p[1] == n.Y || p[1] == n.Y+n.Height) && p[0] >= n.X && p[0] <= n.X+n.Width)
}
func intersectsBox(r Rect, n Node) bool {
	return r.X <= n.X+n.Width && r.X+r.Width >= n.X && r.Y <= n.Y+n.Height && r.Y+r.Height >= n.Y
}
func intersectsLine(r Rect, a, b [2]float64) bool {
	if a[0] == b[0] {
		return a[0] >= r.X && a[0] <= r.X+r.Width && math.Max(a[1], b[1]) >= r.Y && math.Min(a[1], b[1]) <= r.Y+r.Height
	}
	return a[1] >= r.Y && a[1] <= r.Y+r.Height && math.Max(a[0], b[0]) >= r.X && math.Min(a[0], b[0]) <= r.X+r.Width
}

func bindTarget(views map[string]Layout, target map[string]any) (map[string]any, error) {
	diagram, _ := target["diagram_id"].(string)
	view, ok := views[diagram]
	if !ok || target["layout_version"] != view.Version {
		return nil, errors.New("drawing geometry changed; select the target again")
	}
	kind, _ := target["kind"].(string)
	if kind == "component" || kind == "connection" {
		if !onlyKeys(target, "kind", "id", "diagram_id", "layout_version") || len(target) != 4 {
			return nil, errors.New("invalid element selection fields")
		}
		id, _ := target["id"].(string)
		found := false
		if kind == "component" {
			for _, n := range view.Nodes {
				found = found || n.ID == id
			}
		} else {
			for _, e := range view.Edges {
				found = found || e.ID == id
			}
		}
		if !found {
			return nil, errors.New("selected object does not exist in this drawing")
		}
		return cloneObject(target)
	}
	if kind != "area" || !onlyKeys(target, "kind", "diagram_id", "layout_version", "rect", "component_ids", "connection_ids", "intersection_rule") {
		return nil, errors.New("invalid selection kind or fields")
	}
	value, ok := target["rect"].(map[string]any)
	if !ok || len(value) != 4 || !onlyKeys(value, "x", "y", "width", "height") {
		return nil, errors.New("invalid area coordinates")
	}
	var rect Rect
	numbers := []*float64{&rect.X, &rect.Y, &rect.Width, &rect.Height}
	for i, key := range []string{"x", "y", "width", "height"} {
		n, ok := value[key].(json.Number)
		if !ok {
			return nil, errors.New("area coordinates must be numbers")
		}
		f, err := n.Float64()
		if err != nil || !finite(f) {
			return nil, errors.New("nonfinite area coordinates")
		}
		*numbers[i] = f
	}
	if rect.X < 0 || rect.Y < 0 || rect.Width < 5 || rect.Height < 5 || rect.X+rect.Width > view.Width || rect.Y+rect.Height > view.Height {
		return nil, errors.New("selected area is outside drawing bounds")
	}
	nodes, edges := []string{}, []string{}
	for _, n := range view.Nodes {
		if intersectsBox(rect, n) {
			nodes = append(nodes, n.ID)
		}
	}
	for _, e := range view.Edges {
		for i := 1; i < len(e.Points); i++ {
			if intersectsLine(rect, e.Points[i-1], e.Points[i]) {
				edges = append(edges, e.ID)
				break
			}
		}
	}
	if len(nodes)+len(edges) == 0 {
		return nil, errors.New("selected area contains no architecture objects")
	}
	for key, expected := range map[string][]string{"component_ids": nodes, "connection_ids": edges} {
		if actual, exists := target[key]; exists {
			raw, _ := json.Marshal(actual)
			var ids []string
			if json.Unmarshal(raw, &ids) != nil || !reflect.DeepEqual(ids, expected) {
				return nil, errors.New("area object IDs differ from authoritative geometry")
			}
		}
	}
	if rule, exists := target["intersection_rule"]; exists && rule != "bounding-box-or-path-intersection" {
		return nil, errors.New("area intersection rule changed")
	}
	return map[string]any{"kind": "area", "diagram_id": diagram, "layout_version": view.Version, "rect": value, "component_ids": nodes, "connection_ids": edges, "intersection_rule": "bounding-box-or-path-intersection"}, nil
}

func selectionFor(a map[string]any, views map[string]Layout, target map[string]any) map[string]any {
	ids := map[string]map[string]bool{"components": {}, "connections": {}}
	if target["kind"] == "component" {
		ids["components"][target["id"].(string)] = true
	} else if target["kind"] == "connection" {
		ids["connections"][target["id"].(string)] = true
	} else {
		for key, field := range map[string]string{"components": "component_ids", "connections": "connection_ids"} {
			raw, _ := json.Marshal(target[field])
			var list []string
			_ = json.Unmarshal(raw, &list)
			for _, id := range list {
				ids[key][id] = true
			}
		}
	}
	selection := map[string]any{"anchor": target, "geometry": views[target["diagram_id"].(string)]}
	for _, key := range []string{"components", "connections"} {
		values := []any{}
		for _, value := range a[key].([]any) {
			if ids[key][value.(map[string]any)["id"].(string)] {
				values = append(values, value)
			}
		}
		selection[key] = values
	}
	return selection
}

func layoutIDs(views map[string]Layout) []string {
	ids := []string{}
	for id := range views {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
