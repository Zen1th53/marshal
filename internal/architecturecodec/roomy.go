package architecturecodec

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"strings"
	"unicode"
)

// RendererOptions selects the compound renderer for callers that opt in.
type RendererOptions struct{ Roomy bool }

// CompoundNode includes the declared hierarchy rather than projecting endpoints
// to their nearest visible ancestor. Containers are sized after their children.
type CompoundNode struct {
	Node
	Parent, Title, Subtitle string
	Depth                   int
	Area                    bool
}
type CompoundLayout struct {
	Width, Height float64
	Nodes         []CompoundNode
	Edges         []Edge
}

// textExtent uses a conservative advance for the renderer's fixed font. Wide
// scripts reserve a full em; combining marks do not consume another cell.
func textExtent(s string) float64 {
	w := 0.0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r > 255:
			w += 14
		default:
			w += 8.5
		}
	}
	return math.Ceil(w)
}

// RoomyLayout reserves two vertical tracks per connection and a separate label
// row above the compound tree. Tracks stay outside every block. Horizontal
// access runs through container planes only, at the endpoint's header height.
// This intentionally spends space to keep arbitrary cycles and parallel edges
// legible, without scaling the diagram down.
func RoomyLayout(raw json.RawMessage) (CompoundLayout, error) {
	a, err := decodeObject(raw)
	if err != nil {
		return CompoundLayout{}, err
	}
	if err = validateArchitecture(a); err != nil {
		return CompoundLayout{}, err
	}
	children := map[string][]string{}
	objects := map[string]map[string]any{}
	for _, v := range a["components"].([]any) {
		c := v.(map[string]any)
		id := c["id"].(string)
		p, _ := c["parent"].(string)
		children[p] = append(children[p], id)
		objects[id] = c
	}
	ports := map[string]int{}
	for _, v := range a["connections"].([]any) {
		c := v.(map[string]any)
		ports[c["source"].(string)]++
		ports[c["target"].(string)]++
	}
	header := func(id string) float64 { return math.Max(96, 40+float64(ports[id])*16) }
	sizes := map[string][2]float64{}
	var measure func(string) [2]float64
	measure = func(id string) [2]float64 {
		c := objects[id]
		w := math.Max(260, math.Max(textExtent(c["name"].(string))+90, textExtent(c["technology"].(string))+64))
		h := header(id)
		for _, child := range children[id] {
			s := measure(child)
			w = math.Max(w, s[0]+64)
			h += s[1] + 32
		}
		if len(children[id]) > 0 {
			h += 16
		}
		sizes[id] = [2]float64{w, h}
		return sizes[id]
	}
	for _, id := range children[""] {
		measure(id)
	}
	edges := a["connections"].([]any)
	top := 64 + float64(len(edges))*40
	l := CompoundLayout{}
	boxes := map[string]Node{}
	var place func(string, float64, float64, int)
	place = func(id string, x, y float64, depth int) {
		c := objects[id]
		s := sizes[id]
		p, _ := c["parent"].(string)
		n := Node{ID: id, X: x, Y: y, Width: s[0], Height: s[1]}
		boxes[id] = n
		l.Nodes = append(l.Nodes, CompoundNode{Node: n, Parent: p, Title: c["name"].(string), Subtitle: c["technology"].(string), Depth: depth, Area: depth == 0 && len(children[id]) > 0})
		cy := y + header(id)
		for _, child := range children[id] {
			place(child, x+32, cy, depth+1)
			cy += sizes[child][1] + 32
		}
	}
	y := top
	right := 0.0
	for _, id := range children[""] {
		place(id, 48, y, 0)
		s := sizes[id]
		y += s[1] + 48
		right = math.Max(right, 48+s[0])
	}
	labelWidth := 0.0
	for _, v := range edges {
		c := v.(map[string]any)
		labelWidth = math.Max(labelWidth, textExtent(roomyCaption(c))+32)
	}
	usedPorts := map[string]int{}
	portY := func(n Node) float64 { usedPorts[n.ID]++; return n.Y + 24 + float64(usedPorts[n.ID])*16 }
	for i, v := range edges {
		c := v.(map[string]any)
		s, t := boxes[c["source"].(string)], boxes[c["target"].(string)]
		sx := right + 40 + float64(i)*24
		tx := right + 64 + float64(len(edges))*48 + labelWidth - float64(i)*24
		ly := 48 + float64(i)*40
		sy, ty := portY(s), portY(t)
		l.Edges = append(l.Edges, Edge{ID: c["id"].(string), Caption: roomyCaption(c), Points: [][2]float64{{s.X + s.Width, sy}, {sx, sy}, {sx, ly}, {tx, ly}, {tx, ty}, {t.X + t.Width, ty}}, LabelAt: [2]float64{(sx + tx) / 2, ly - 8}})
	}
	l.Width = right + 112 + float64(len(edges))*48 + labelWidth
	l.Height = y + 16
	return l, nil
}
func roomyCaption(c map[string]any) string {
	parts := []string{c["label"].(string)}
	for _, key := range []string{"protocol", "access"} {
		if s, ok := c[key].(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

// RenderSVG is the shared architecture renderer. The option is explicit so
// existing geometry consumers retain their original drawings.
func RenderSVG(raw json.RawMessage, options RendererOptions) (string, error) {
	if !options.Roomy {
		return "", fmt.Errorf("compound SVG requires the roomy renderer option")
	}
	l, err := RoomyLayout(raw)
	if err != nil {
		return "", err
	}
	return compoundSVG(l), nil
}
func svgText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func compoundSVG(l CompoundLayout) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg id="roomy-svg" xmlns="http://www.w3.org/2000/svg" width="%g" height="%g" viewBox="0 0 %g %g" role="img" aria-label="Architecture"><style>#roomy-svg text{font-family:monospace;font-size:14px;fill:#dce7f5}.roomy-title{font-weight:bold}#roomy-svg [data-id]{cursor:pointer}#roomy-svg [data-id]:focus>rect,#roomy-svg [data-id].chosen>rect{stroke:#9fc8ff;stroke-width:3}#roomy-svg .roomy-edge.chosen path{stroke:#9fc8ff;stroke-width:3}.roomy-subtitle{fill:#a9bbd2;font-size:12px}.roomy-edge path{fill:none;stroke:#8ca8c9;stroke-width:1.5}.roomy-label{paint-order:stroke;stroke:#0c121c;stroke-width:6px;stroke-linejoin:round}</style><defs><marker id="roomy-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto"><path d="M0 0L10 5L0 10Z" fill="#8ca8c9"/></marker></defs>`, l.Width, l.Height, l.Width, l.Height)
	colors := []string{"#203446", "#302e46", "#243d37"}
	for _, n := range l.Nodes {
		kind := "block"
		fill := "#142131"
		if n.Area {
			kind = "area"
			fill = colors[len(n.ID)%len(colors)]
		} else if n.Depth > 1 {
			kind = "chip"
		}
		id := svgText(n.ID)
		fmt.Fprintf(&b, `<g id="roomy-node-%s" data-id="%s" data-kind="component" data-role="%s" data-parent="%s"><rect x="%g" y="%g" width="%g" height="%g" rx="12" fill="%s" stroke="#58708d"/>`, id, id, kind, svgText(n.Parent), n.X, n.Y, n.Width, n.Height, fill)
		fmt.Fprintf(&b, `<g transform="translate(%g %g)" fill="none" stroke="#a9bbd2" stroke-width="1.5" aria-hidden="true"><rect width="20" height="18" rx="3"/><path d="M0 6H20M5 3H6M9 3H10"/></g>`, n.X+20, n.Y+17)
		fmt.Fprintf(&b, `<text class="roomy-title" x="%g" y="%g">%s</text><text class="roomy-subtitle" x="%g" y="%g">%s</text></g>`, n.X+50, n.Y+32, svgText(n.Title), n.X+20, n.Y+58, svgText(n.Subtitle))
	}
	for _, e := range l.Edges {
		id := svgText(e.ID)
		source, target := "", ""
		for _, n := range l.Nodes {
			if onBoundary(e.Points[0], n.Node) {
				source = n.ID
			}
			if onBoundary(e.Points[len(e.Points)-1], n.Node) {
				target = n.ID
			}
		}
		fmt.Fprintf(&b, `<g id="roomy-edge-%s" class="roomy-edge" data-kind="connection" data-id="%s" data-from="%s" data-to="%s"><path d="`, id, id, svgText(source), svgText(target))
		for i, p := range e.Points {
			op := "L"
			if i == 0 {
				op = "M"
			}
			fmt.Fprintf(&b, "%s%g %g ", op, p[0], p[1])
		}
		fmt.Fprintf(&b, `" marker-end="url(#roomy-arrow)"/><text class="roomy-label" x="%g" y="%g" text-anchor="middle">%s</text></g>`, e.LabelAt[0], e.LabelAt[1], svgText(e.Caption))
	}
	b.WriteString("</svg>")
	return b.String()
}
