package architecturecodec

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"sort"
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

// RoomyLayout lays out area ranks from left to right. A minimum feedback
// ordering chooses cycle reversals; only routing, never the contract, is reversed.
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
	roots := children[""]
	rootOf := func(id string) string {
		for {
			p, _ := objects[id]["parent"].(string)
			if p == "" {
				return id
			}
			id = p
		}
	}
	edges := a["connections"].([]any)
	weights := make([][]int, len(roots))
	index := map[string]int{}
	for i, id := range roots {
		index[id] = i
		weights[i] = make([]int, len(roots))
	}
	for _, v := range edges {
		c := v.(map[string]any)
		s, t := index[rootOf(c["source"].(string))], index[rootOf(c["target"].(string))]
		if s != t {
			weights[s][t]++
		}
	}
	order := feedbackOrder(weights)
	ranks := make([]int, len(roots))
	for i, u := range order {
		for _, v := range order[i+1:] {
			if weights[u][v]+weights[v][u] > 0 && ranks[v] <= ranks[u] {
				ranks[v] = ranks[u] + 1
			}
		}
	}
	captionWidth := 120.0
	for _, v := range edges {
		captionWidth = math.Max(captionWidth, textExtent(roomyCaption(v.(map[string]any)))+32)
	}
	// Every vertical track is unique and 20px from its neighbour. The two
	// horizontal access segments each have room for a complete caption.
	gutter := 2*captionWidth + 80 + float64(len(edges))*20
	sizes := map[string][2]float64{}
	subtitle := func(c map[string]any) string {
		for _, key := range []string{"description", "responsibility", "owned_path", "kind", "technology"} {
			if v, ok := c[key].(string); ok && strings.TrimSpace(v) != "" && v != "t" {
				return strings.Join(strings.Fields(v), " ")
			}
		}
		return ""
	}
	var measure func(string, int) [2]float64
	measure = func(id string, depth int) [2]float64 {
		c := objects[id]
		w := math.Max(280, math.Max(textExtent(c["name"].(string))+76, textExtent(subtitle(c))+40))
		h := 100.0
		if depth > 1 {
			w = math.Max(100, textExtent(c["name"].(string))+24)
			h = 30
		}
		if depth == 1 && len(children[id]) > 0 {
			chipW := 100.0
			for _, ch := range children[id] {
				z := measure(ch, depth+1)
				chipW = math.Max(chipW, z[0])
			}
			w = math.Max(w, 2*chipW+52)
			h = 100 + float64((len(children[id])+1)/2)*42
		}
		sizes[id] = [2]float64{w, h}
		return sizes[id]
	}
	colWidths := map[int]float64{}
	for _, id := range roots {
		if len(children[id]) == 0 {
			measure(id, 1)
		} else {
			w, h := 0.0, 80.0
			cols := 1
			if len(children[id]) > 4 {
				cols = 2
			}
			rowH := 0.0
			for i, ch := range children[id] {
				z := measure(ch, 1)
				w = math.Max(w, z[0])
				rowH = math.Max(rowH, z[1])
				if i%cols == cols-1 || i == len(children[id])-1 {
					h += rowH + 72
					rowH = 0
				}
			}
			sizes[id] = [2]float64{float64(cols)*w + float64(cols-1)*gutter + 64, h}
		}
		r := ranks[index[id]]
		colWidths[r] = math.Max(colWidths[r], sizes[id][0])
	}
	colX := map[int]float64{}
	x := 48.0 + float64(len(edges))*20
	for r := 0; r <= len(roots); r++ {
		if w := colWidths[r]; w > 0 {
			colX[r] = x
			x += w + gutter
		}
	}
	l := CompoundLayout{Width: x - gutter + 48}
	boxes := map[string]Node{}
	colY := map[int]float64{}
	var place func(string, float64, float64, int)
	place = func(id string, x, y float64, depth int) {
		c := objects[id]
		z := sizes[id]
		p, _ := c["parent"].(string)
		n := Node{ID: id, X: x, Y: y, Width: z[0], Height: z[1]}
		boxes[id] = n
		l.Nodes = append(l.Nodes, CompoundNode{Node: n, Parent: p, Title: c["name"].(string), Subtitle: subtitle(c), Depth: depth, Area: depth == 0 && len(children[id]) > 0})
		if depth > 1 {
			return
		}
		cols := 1
		if depth == 0 && len(children[id]) > 4 || depth == 1 {
			cols = 2
		}
		cy := y + 80
		if depth == 1 {
			cy = y + 94
		}
		cw := 0.0
		for _, ch := range children[id] {
			cw = math.Max(cw, sizes[ch][0])
		}
		rowH := 0.0
		for i, ch := range children[id] {
			gap := gutter
			if depth == 1 {
				gap = 12
			}
			place(ch, x+32+float64(i%cols)*(cw+gap), cy, depth+1)
			rowH = math.Max(rowH, sizes[ch][1])
			if i%cols == cols-1 {
				cy += rowH + 72
				if depth == 1 {
					cy -= 60
				}
				rowH = 0
			}
		}
	}
	for _, u := range order {
		id := roots[u]
		r := ranks[u]
		y := colY[r]
		if y == 0 {
			y = 64
		}
		depth := 0
		if len(children[id]) == 0 {
			depth = 1
		}
		place(id, colX[r], y, depth)
		colY[r] = y + sizes[id][1] + 64
		l.Height = math.Max(l.Height, colY[r])
	}
	// Routes to chips use the chip's actual border, preserving selectable endpoints.
	used := map[string]int{}
	occupiedY := map[float64]bool{}
	port := func(n Node) float64 {
		used[n.ID]++
		y := n.Y + 8 + math.Mod(float64(used[n.ID])*18, n.Height-16)
		for occupiedY[y] {
			y += 0.25
		}
		occupiedY[y] = true
		return y
	}

	var labels []Rect
	overlaps := func(a, b Rect) bool {
		return a.X < b.X+b.Width && a.X+a.Width > b.X && a.Y < b.Y+b.Height && a.Y+a.Height > b.Y
	}
	bottom := l.Height + 32
	usedChannels := map[float64]bool{}
	bottomLane := 0
	parents := ParentsOf(a)
	for i, v := range edges {
		c := v.(map[string]any)
		s, t := boxes[c["source"].(string)], boxes[c["target"].(string)]
		sy, ty := port(s), port(t)
		sx, tx := s.X+s.Width, t.X
		caption := roomyCaption(c)
		lw := textExtent(caption) + 24
		var points [][2]float64
		if tx > sx && ranks[index[rootOf(s.ID)]]+1 == ranks[index[rootOf(t.ID)]] {
			lane := sx + captionWidth + 40 + float64(i)*20
			points = [][2]float64{{sx, sy}, {lane, sy}, {lane, ty}, {tx, ty}}
		}
		blocked := len(points) == 0
		for j := 1; j < len(points); j++ {
			for _, n := range l.Nodes {
				if n.Area || n.ID == s.ID || n.ID == t.ID || Contains(parents, n.ID, s.ID) || Contains(parents, n.ID, t.ID) {
					continue
				}
				if roomyIntersects([2][2]float64{points[j-1], points[j]}, n.Node) {
					blocked = true
				}
			}
		}
		if blocked {
			// Non-adjacent and backward connections use a horizontal channel below
			// the planes; access tracks remain just outside their endpoint columns.
			channel := bottom + float64(bottomLane)*36
			// Forward skips and intra-area edges can use a free row channel.
			// Backward inter-area edges stay below the planes.
			if tx >= sx || rootOf(s.ID) == rootOf(t.ID) {
				var candidates []float64
				for _, n := range l.Nodes {
					if !n.Area {
						candidates = append(candidates, n.Y+n.Height+24, n.Y+n.Height+52)
					}
				}
				sort.SliceStable(candidates, func(i, j int) bool { return math.Abs(candidates[i]-(sy+ty)/2) < math.Abs(candidates[j]-(sy+ty)/2) })
				for _, cy := range candidates {
					clear := true
					for y := range usedChannels {
						if math.Abs(y-cy) < 28 {
							clear = false
						}
					}
					for _, n := range l.Nodes {
						if n.Area {
							continue
						}
						if roomyIntersects([2][2]float64{{sx + 24 + float64(i)*20, cy}, {tx - 24 - float64(i)*20, cy}}, n.Node) {
							clear = false
						}
					}
					for _, n := range l.Nodes {
						if !n.Area && cy+13 > n.Y && cy-13 < n.Y+n.Height {
							clear = false
						}
					}
					for _, r := range labels {
						if cy+13 > r.Y && cy-13 < r.Y+r.Height {
							clear = false
						}
					}
					if clear {
						channel = cy
						break
					}
				}
			}
			if channel >= bottom {
				bottomLane++
			}
			usedChannels[channel] = true
			points = [][2]float64{{sx, sy}, {sx + 24 + float64(i)*20, sy}, {sx + 24 + float64(i)*20, channel}, {tx - 24 - float64(i)*20, channel}, {tx - 24 - float64(i)*20, ty}, {tx, ty}}
			l.Height = math.Max(l.Height, channel+40)
			l.Width = math.Max(l.Width, sx+64+float64(i)*20)
		}
		// Search horizontal segments longest-first, then slide to the next free
		// slot. Area planes are transparent; cards and chips are obstacles.
		type slot struct {
			a, b   [2]float64
			length float64
		}
		var slots []slot
		for j := 1; j < len(points); j++ {
			p, q := points[j-1], points[j]
			if p[1] == q[1] {
				slots = append(slots, slot{p, q, math.Abs(p[0] - q[0])})
			}
		}
		sort.SliceStable(slots, func(i, j int) bool { return slots[i].length > slots[j].length })
		at := [2]float64{}
		found := false
		for _, z := range slots {
			lo, hi := math.Min(z.a[0], z.b[0]), math.Max(z.a[0], z.b[0])
			for step := 0; step < 100; step++ {
				shift := float64((step+1)/2) * 28
				if step%2 == 1 {
					shift = -shift
				}
				cx := (lo+hi)/2 + shift
				if cx-lw/2 < lo || cx+lw/2 > hi {
					continue
				}
				rect := Rect{cx - lw/2, z.a[1] - 13, lw, 26}
				clear := true
				for _, n := range l.Nodes {
					if !n.Area && overlaps(rect, Rect{n.X, n.Y, n.Width, n.Height}) {
						clear = false
					}
				}
				for _, r := range labels {
					if overlaps(rect, r) {
						clear = false
					}
				}
				if clear {
					at = [2]float64{cx, z.a[1] + 5}
					labels = append(labels, rect)
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return CompoundLayout{}, fmt.Errorf("no free label slot for %s", c["id"])
		}
		l.Edges = append(l.Edges, Edge{ID: c["id"].(string), Caption: caption, Points: points, LabelAt: at})
	}
	return l, nil
}

// Dynamic programming gives an exact minimum feedback arc ordering for up to
// 18 areas. Larger graphs use exact branch-and-bound to limit memory.
func feedbackOrder(w [][]int) []int {
	n := len(w)
	if n > 18 {
		if order, ok := feedbackSCCOrder(w); ok {
			return order
		}
		order := []int{}
		for u := 0; u < n; u++ {
			best, cost := 0, int(^uint(0)>>1)
			for p := 0; p <= len(order); p++ {
				c := 0
				for j, v := range order {
					if j < p {
						c += w[u][v]
					} else {
						c += w[v][u]
					}
				}
				if c < cost {
					best, cost = p, c
				}
			}
			order = append(order, 0)
			copy(order[best+1:], order[best:])
			order[best] = u
		}
		bestCost := 0
		for i, u := range order {
			for _, v := range order[i+1:] {
				bestCost += w[v][u]
			}
		}
		chosen := make([]bool, n)
		prefix := []int{}
		var search func(int)
		search = func(cost int) {
			lower := cost
			for u := 0; u < n; u++ {
				if chosen[u] {
					continue
				}
				for _, v := range prefix {
					lower += w[u][v]
				}
				for v := u + 1; v < n; v++ {
					if !chosen[v] {
						lower += min(w[u][v], w[v][u])
					}
				}
			}
			if lower >= bestCost {
				return
			}
			if len(prefix) == n {
				bestCost = cost
				order = append([]int(nil), prefix...)
				return
			}
			for u := 0; u < n; u++ {
				if chosen[u] {
					continue
				}
				extra := 0
				for _, v := range prefix {
					extra += w[u][v]
				}
				chosen[u] = true
				prefix = append(prefix, u)
				search(cost + extra)
				prefix = prefix[:len(prefix)-1]
				chosen[u] = false
			}
		}
		search(0)

		return order
	}
	dp := make([]int, 1<<n)
	last := make([]int, 1<<n)
	for mask := 1; mask < len(dp); mask++ {
		dp[mask] = int(^uint(0) >> 1)
		for u := 0; u < n; u++ {
			if mask&(1<<u) == 0 {
				continue
			}
			prev := mask &^ (1 << u)
			cost := dp[prev]
			for v := 0; v < n; v++ {
				if prev&(1<<v) != 0 {
					cost += w[u][v]
				}
			}
			if cost < dp[mask] {
				dp[mask] = cost
				last[mask] = u
			}
		}
	}
	order := make([]int, n)
	mask := len(dp) - 1
	for i := n - 1; i >= 0; i-- {
		order[i] = last[mask]
		mask &^= 1 << order[i]
	}
	return order
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
	colors := []string{"#d99a58", "#64bd94", "#ab83df", "#679bda", "#bf8fa8"}
	areaIndex := 0
	b.WriteString(`<rect width="100%" height="100%" fill="#0d0f13"/>`)
	for _, n := range l.Nodes {
		kind := "block"
		fill := "#181d26"
		stroke := "#3a414e"
		if n.Area {
			kind = "area"
			stroke = colors[areaIndex%len(colors)]
			areaIndex++
			fill = "#15181e"
		} else if n.Depth > 1 {
			kind = "chip"
		}
		id := svgText(n.ID)
		fmt.Fprintf(&b, `<g id="roomy-node-%s" data-id="%s" data-kind="component" data-role="%s" data-parent="%s"><rect x="%g" y="%g" width="%g" height="%g" rx="10" fill="%s" stroke="%s"/>`, id, id, kind, svgText(n.Parent), n.X, n.Y, n.Width, n.Height, fill, stroke)
		if n.Area {
			fmt.Fprintf(&b, `<rect x="%g" y="%g" width="%g" height="30" rx="6" fill="#0d0f13" stroke="%s"/><text x="%g" y="%g" style="fill:%s;font-weight:bold">%s</text></g>`, n.X+16, n.Y+14, textExtent(n.Title)+28, stroke, n.X+30, n.Y+34, stroke, svgText(strings.ToUpper(n.Title)))
			continue
		}
		if n.Depth > 1 {
			fmt.Fprintf(&b, `<text x="%g" y="%g" text-anchor="middle">%s</text></g>`, n.X+n.Width/2, n.Y+20, svgText(n.Title))
			continue
		}
		icon := "M2 2H22V20H2ZM2 8H22M6 5H7M10 5H11"
		name := strings.ToLower(n.Title + " " + n.Subtitle)
		switch {
		case strings.Contains(name, "database") || strings.Contains(name, "storage") || strings.Contains(name, "cache"):
			icon = "M2 5C2 0 22 0 22 5V19C22 24 2 24 2 19ZM2 5C2 10 22 10 22 5M2 12C2 17 22 17 22 12"
		case strings.Contains(name, "worker"):
			icon = "M4 4H20V20H4ZM8 0V4M16 0V4M8 20V24M16 20V24M0 8H4M20 8H24M0 16H4M20 16H24"
		case strings.Contains(name, "auth"):
			icon = "M5 10V6C5 -1 19 -1 19 6V10M3 10H21V23H3Z"
		case strings.Contains(name, "console") || strings.Contains(name, "terminal"):
			icon = "M2 2H22V22H2ZM6 7L11 12L6 17M13 17H19"
		}
		fmt.Fprintf(&b, `<path transform="translate(%g %g)" d="%s" fill="none" stroke="#b7becb" stroke-width="1.5" aria-hidden="true"/>`, n.X+18, n.Y+17, icon)

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
		b.WriteString(`" marker-end="url(#roomy-arrow)"/></g>`)
	}
	for _, e := range l.Edges {
		fmt.Fprintf(&b, `<g class="roomy-caption" data-edge="%s"><rect x="%g" y="%g" width="%g" height="26" rx="7" fill="#10141b" stroke="#3a414e"/><text x="%g" y="%g" text-anchor="middle">%s</text></g>`, svgText(e.ID), e.LabelAt[0]-textExtent(e.Caption)/2-12, e.LabelAt[1]-18, textExtent(e.Caption)+24, e.LabelAt[0], e.LabelAt[1], svgText(e.Caption))
	}

	b.WriteString("</svg>")
	return b.String()
}

func roomyIntersects(s [2][2]float64, n Node) bool {
	a, b := s[0], s[1]
	if a[0] == b[0] {
		return a[0] > n.X && a[0] < n.X+n.Width && math.Max(a[1], b[1]) > n.Y && math.Min(a[1], b[1]) < n.Y+n.Height
	}
	return a[1] > n.Y && a[1] < n.Y+n.Height && math.Max(a[0], b[0]) > n.X && math.Min(a[0], b[0]) < n.X+n.Width
}

// Independent strongly connected components can be optimized separately.
// Their condensation is acyclic, so no cross-component edge needs reversing.
func feedbackSCCOrder(w [][]int) ([]int, bool) {
	n := len(w)
	reach := make([][]bool, n)
	for i := range w {
		reach[i] = make([]bool, n)
		for j := range w[i] {
			reach[i][j] = i == j || w[i][j] > 0
		}
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				reach[i][j] = reach[i][j] || reach[i][k] && reach[k][j]
			}
		}
	}
	groups := [][]int{}
	groupOf := make([]int, n)
	for i := range groupOf {
		groupOf[i] = -1
	}
	for u := 0; u < n; u++ {
		if groupOf[u] >= 0 {
			continue
		}
		g := []int{}
		for v := u; v < n; v++ {
			if reach[u][v] && reach[v][u] {
				groupOf[v] = len(groups)
				g = append(g, v)
			}
		}
		groups = append(groups, g)
	}
	if len(groups) == 1 {
		return nil, false
	}
	incoming := make([]int, len(groups))
	for u := range w {
		for v, c := range w[u] {
			if c > 0 && groupOf[u] != groupOf[v] {
				incoming[groupOf[v]]++
			}
		}
	}
	result := []int{}
	done := make([]bool, len(groups))
	for count := 0; count < len(groups); count++ {
		for g, members := range groups {
			if done[g] || incoming[g] != 0 {
				continue
			}
			done[g] = true
			sub := make([][]int, len(members))
			for i, u := range members {
				sub[i] = make([]int, len(members))
				for j, v := range members {
					sub[i][j] = w[u][v]
				}
			}
			for _, i := range feedbackOrder(sub) {
				result = append(result, members[i])
			}
			for _, u := range members {
				for v, c := range w[u] {
					if c > 0 && groupOf[v] != g {
						incoming[groupOf[v]]--
					}
				}
			}
			break
		}
	}
	return result, true
}
